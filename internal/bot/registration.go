package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

const (
	stateRegFirst = "reg_first"
	stateRegLast  = "reg_last"
	stateRegPhone = "reg_phone"
	stateRegPVZ   = "reg_pvz"
)

func (b *Bot) onStart(ctx context.Context, msg *tgbotapi.Message, payload string) error {
	tgID := msg.From.ID
	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}

	user, err := b.store.UserByTG(ctx, tgID)
	if err == nil {
		return b.showMenu(ctx, tgID, fmt.Sprintf("Привет, %s! Главное меню ниже.", esc(user.FirstName)))
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeOpen {
		b.send(tgID, "Привет! Сейчас регистрация на обмен подарками закрыта.", tgbotapi.NewRemoveKeyboard(true))
		return nil
	}
	if b.cfg.InviteCode != "" && payload != b.cfg.InviteCode && !b.cfg.IsAdmin(tgID) {
		b.send(tgID, "Привет! Чтобы участвовать, открой бота по ссылке-приглашению от организатора.", tgbotapi.NewRemoveKeyboard(true))
		return nil
	}

	b.send(tgID, "Привет! Это обмен подарками между детьми. Сначала зарегистрируемся: это один взрослый на семью.", nil)
	return b.startRegistration(ctx, tgID, false)
}

func (b *Bot) startRegistration(ctx context.Context, tgID int64, edit bool) error {
	data := map[string]string{}
	if edit {
		data["edit"] = "1"
	}
	if err := b.store.SaveSession(ctx, tgID, store.Session{State: stateRegFirst, Data: data}); err != nil {
		return err
	}
	b.send(tgID, "Как тебя зовут? Напиши <b>имя</b>.", cancelKeyboard())
	return nil
}

func (b *Bot) handleRegistrationStep(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	switch sess.State {
	case stateRegFirst:
		if !validName(text) {
			b.send(tgID, "Имя должно быть от 1 до 100 символов. Попробуй ещё раз.", nil)
			return nil
		}
		sess.Data["first_name"] = text
		sess.State = stateRegLast
		b.send(tgID, "Теперь <b>фамилию</b>.", nil)

	case stateRegLast:
		if !validName(text) {
			b.send(tgID, "Фамилия должна быть от 1 до 100 символов. Попробуй ещё раз.", nil)
			return nil
		}
		sess.Data["last_name"] = text
		sess.State = stateRegPhone
		kb := tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButtonContact("📱 Отправить мой номер")),
			tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(btnCancel)),
		)
		kb.ResizeKeyboard = true
		b.send(tgID, "Нужен телефон, чтобы организатор мог связаться, если с посылкой что-то пойдёт не так. Нажми кнопку или напиши номер.", kb)

	case stateRegPhone:
		phone := text
		if msg.Contact != nil {
			if msg.Contact.UserID != 0 && msg.Contact.UserID != tgID {
				b.send(tgID, "Нужен твой номер, а не чужой контакт.", nil)
				return nil
			}
			phone = msg.Contact.PhoneNumber
		}
		if !validPhone(phone) {
			b.send(tgID, "Не похоже на номер телефона. Нажми кнопку или напиши номер, например +375291234567.", nil)
			return nil
		}
		sess.Data["phone"] = phone
		sess.State = stateRegPVZ
		b.send(tgID, "Последний шаг: <b>адрес ПВЗ Wildberries</b>, куда тебе будут заказывать посылку. Город, улица, дом.", cancelKeyboard())

	case stateRegPVZ:
		if utf8.RuneCountInString(text) < 5 || utf8.RuneCountInString(text) > 500 {
			b.send(tgID, "Адрес должен быть от 5 до 500 символов. Попробуй ещё раз.", nil)
			return nil
		}
		return b.finishRegistration(ctx, msg, sess, text)
	}
	return b.store.SaveSession(ctx, tgID, sess)
}

func (b *Bot) finishRegistration(ctx context.Context, msg *tgbotapi.Message, sess store.Session, pvz string) error {
	tgID := msg.From.ID
	editing := sess.Data["edit"] == "1"

	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if !editing && ex.Status != domain.ExchangeOpen {
		if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
			return err
		}
		b.send(tgID, "Пока ты заполнял(а) данные, регистрация закрылась. Напиши организатору.", tgbotapi.NewRemoveKeyboard(true))
		return nil
	}

	old, oldErr := b.store.UserByTG(ctx, tgID)
	if oldErr != nil && !errors.Is(oldErr, store.ErrNotFound) {
		return oldErr
	}

	user := store.User{
		TgID:       tgID,
		TgUsername: msg.From.UserName,
		FirstName:  sess.Data["first_name"],
		LastName:   sess.Data["last_name"],
		Phone:      sess.Data["phone"],
		PVZAddress: pvz,
	}
	if err := b.store.SaveUser(ctx, user); err != nil {
		return err
	}
	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}

	if editing {
		if oldErr == nil && old.PVZAddress != pvz {
			b.notifyGiversAboutAddress(ctx, ex, old.ID, pvz)
		}
		return b.showMenu(ctx, tgID, "Данные обновлены.")
	}

	b.send(tgID, "Готово, ты зарегистрирован(а)! Теперь добавь детей, которые участвуют в обмене.", mainMenu())
	saved, err := b.store.UserByTG(ctx, tgID)
	if err != nil {
		return err
	}
	return b.showChildren(ctx, saved)
}

// notifyGiversAboutAddress warns givers who may already have ordered to the old address.
func (b *Bot) notifyGiversAboutAddress(ctx context.Context, ex store.Exchange, parentID int64, pvz string) {
	if ex.Status != domain.ExchangeDistributed {
		return
	}
	list, err := b.store.AssignmentsByParent(ctx, ex.ID, parentID)
	if err != nil {
		return
	}
	for _, a := range list {
		if a.Status == domain.AssignmentReceived {
			continue
		}
		b.send(a.Giver.TgID, fmt.Sprintf("⚠️ У получателя подарка для <b>%s</b> изменился адрес ПВЗ:\n%s\n\nЕсли ещё не заказал(а), заказывай на новый адрес.",
			esc(a.ChildFirstName), esc(pvz)), nil)
	}
	b.notifyAdmins(fmt.Sprintf("Участник (user %d) сменил адрес ПВЗ после распределения.", parentID))
}

func (b *Bot) showProfile(ctx context.Context, user store.User) error {
	text := fmt.Sprintf("<b>Мой профиль</b>\n\n%s %s\nТелефон: %s\nПВЗ Wildberries: %s",
		esc(user.FirstName), esc(user.LastName), esc(user.Phone), esc(user.PVZAddress))
	b.send(user.TgID, text, inlineButtons(button("✏️ Изменить данные", "prof", 0)))
	return nil
}

func validName(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= 100 && !strings.HasPrefix(s, "/")
}

func validPhone(s string) bool {
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case strings.ContainsRune("+ -()", r):
		default:
			return false
		}
	}
	return digits >= 7 && digits <= 15
}
