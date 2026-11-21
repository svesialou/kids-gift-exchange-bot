package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

const (
	stateOrderDate = "order_date"
	stateCode      = "code"
)

func (b *Bot) distributedExchange(ctx context.Context, tgID int64) (store.Exchange, bool, error) {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return store.Exchange{}, false, err
	}
	if ex.Status != domain.ExchangeDistributed && ex.Status != domain.ExchangeCompleted {
		b.send(tgID, "Распределение ещё не проводилось. Бот напишет, когда оно будет готово.", nil)
		return ex, false, nil
	}
	return ex, true, nil
}

// ---------- giver side ----------

func (b *Bot) showGiverCards(ctx context.Context, user store.User) error {
	ex, ok, err := b.distributedExchange(ctx, user.TgID)
	if err != nil || !ok {
		return err
	}
	list, err := b.store.AssignmentsByGiver(ctx, ex.ID, user.ID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		b.send(user.TgID, "Тебе не досталось детей в этом обмене.", nil)
		return nil
	}
	for _, a := range list {
		b.sendGiverCard(a, "")
	}
	return nil
}

func (b *Bot) giverCardText(a store.Assignment) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🎁 <b>%s %s</b>, %s, %s\n", esc(a.ChildFirstName), esc(a.ChildLastName), ageText(a.ChildAge), genderText(a.ChildGender))
	if a.ChildWishes != "" {
		fmt.Fprintf(&sb, "Пожелания: %s\n", esc(a.ChildWishes))
	}
	fmt.Fprintf(&sb, "\nПолучатель посылки: %s %s\nПВЗ Wildberries: %s\n", esc(a.Parent.FirstName), esc(a.Parent.LastName), esc(a.Parent.PVZAddress))
	fmt.Fprintf(&sb, "Бюджет: %s. Заказать: %s.\n", b.budgetText(), b.orderDeadlineText())
	fmt.Fprintf(&sb, "\nСтатус: %s", b.statusText(a))
	return sb.String()
}

func (b *Bot) giverButtons(a store.Assignment) any {
	switch a.Status {
	case domain.AssignmentAssigned:
		return inlineButtons(button("✅ Заказал(а)", "ord", a.ID))
	case domain.AssignmentOrdered:
		return inlineButtons(button("📦 Пришло в ПВЗ", "arr", a.ID))
	case domain.AssignmentArrived:
		return inlineButtons(button("🔑 Отправить код получения", "code", a.ID))
	}
	return nil
}

func (b *Bot) sendGiverCard(a store.Assignment, header string) {
	text := b.giverCardText(a)
	if header != "" {
		text = header + "\n\n" + text
	}
	b.send(a.Giver.TgID, text, b.giverButtons(a))
}

func (b *Bot) statusText(a store.Assignment) string {
	switch a.Status {
	case domain.AssignmentAssigned:
		return "ещё не заказан"
	case domain.AssignmentOrdered:
		if a.ExpectedDate != nil {
			return "заказан, ожидается " + a.ExpectedDate.Format("02.01")
		}
		return "заказан"
	case domain.AssignmentArrived:
		if a.PickupState().CodeValid(b.now()) {
			return "ждёт в ПВЗ, код на сегодня отправлен"
		}
		if a.PickupState().CodeRequestPending() {
			return "ждёт в ПВЗ, получатель ждёт код"
		}
		return "ждёт в ПВЗ"
	case domain.AssignmentReceived:
		return "получен ✅"
	}
	return string(a.Status)
}

// giverAssignment loads an assignment and checks that the user is its giver.
func (b *Bot) giverAssignment(ctx context.Context, user store.User, id int64) (store.Assignment, bool, error) {
	a, err := b.store.Assignment(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.Giver.ID != user.ID) {
		b.send(user.TgID, "Этот подарок закреплён не за тобой.", nil)
		return store.Assignment{}, false, nil
	}
	if err != nil {
		return store.Assignment{}, false, err
	}
	return a, true, nil
}

func (b *Bot) startOrdered(ctx context.Context, user store.User, id int64) error {
	a, ok, err := b.giverAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	if a.Status != domain.AssignmentAssigned {
		b.send(user.TgID, "Этот подарок уже отмечен как заказанный.", nil)
		return nil
	}
	sess := store.Session{State: stateOrderDate, Data: map[string]string{"aid": strconv.FormatInt(id, 10)}}
	if err := b.store.SaveSession(ctx, user.TgID, sess); err != nil {
		return err
	}
	b.send(user.TgID, fmt.Sprintf("Когда примерно доставка подарка для %s? Напиши дату в формате ДД.ММ, например 15.12.", esc(a.ChildFirstName)),
		stepKeyboard(btnDontKnow))
	return nil
}

func (b *Bot) handleOrderDate(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	var expected *time.Time
	if text != btnDontKnow {
		d, ok := parseDayMonth(text, b.now().In(b.cfg.Location))
		if !ok {
			b.send(tgID, "Не понял дату. Напиши в формате ДД.ММ, например 15.12, или нажми «Не знаю».", nil)
			return nil
		}
		expected = &d
	}

	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}
	user, err := b.store.UserByTG(ctx, tgID)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(sess.Data["aid"], 10, 64)
	ok, err := b.store.MarkOrdered(ctx, id, user.ID, expected, b.now())
	if err != nil {
		return err
	}
	if !ok {
		return b.showMenu(ctx, tgID, "Не получилось: подарок уже отмечен или закреплён не за тобой.")
	}
	a, err := b.store.Assignment(ctx, id)
	if err != nil {
		return err
	}

	when := "дату доставки пока не знаем"
	if expected != nil {
		when = "ожидаемая дата доставки " + expected.Format("02.01")
	}
	b.send(a.Parent.TgID, fmt.Sprintf("🎁 Подарок для <b>%s</b> заказан, %s. Когда посылка придёт в ПВЗ, я напишу.", esc(a.ChildFirstName), when), nil)
	b.send(tgID, "Отлично, записал. Когда посылка придёт в ПВЗ, нажми «Пришло в ПВЗ».", mainMenu())
	b.sendGiverCard(a, "")
	return nil
}

func (b *Bot) markArrived(ctx context.Context, user store.User, id int64) error {
	ok, err := b.store.MarkArrived(ctx, id, user.ID, b.now())
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, "Не получилось: подарок уже отмечен или закреплён не за тобой.", nil)
		return nil
	}
	a, err := b.store.Assignment(ctx, id)
	if err != nil {
		return err
	}
	b.send(a.Parent.TgID, fmt.Sprintf(
		"📦 Посылка для <b>%s</b> пришла в ПВЗ:\n%s\n\nКогда соберёшься забирать, нажми «Иду забирать», и даритель пришлёт код. Код действует только в день отправки.",
		esc(a.ChildFirstName), esc(a.Parent.PVZAddress)),
		inlineButtons(button("🚶 Иду забирать, нужен код", "pick", a.ID)))
	b.send(user.TgID, "Записал. Получатель попросит код, когда пойдёт забирать, и я сразу напишу тебе. Можно прислать код и сейчас, если получатель собирается забрать сегодня.",
		inlineButtons(button("🔑 Отправить код получения", "code", a.ID)))
	return nil
}

func (b *Bot) startCode(ctx context.Context, user store.User, id int64) error {
	a, ok, err := b.giverAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	if a.Status == domain.AssignmentReceived {
		b.send(user.TgID, "Этот подарок уже получен 🎉", nil)
		return nil
	}
	sess := store.Session{State: stateCode, Data: map[string]string{"aid": strconv.FormatInt(id, 10)}}
	if err := b.store.SaveSession(ctx, user.TgID, sess); err != nil {
		return err
	}
	b.send(user.TgID, fmt.Sprintf(
		"Пришли код получения посылки для %s из приложения Wildberries: текстом или скриншотом QR-кода. Код действует только сегодня.",
		esc(a.ChildFirstName)), cancelKeyboard())
	return nil
}

func (b *Bot) handleCodeInput(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	fileID := ""
	if len(msg.Photo) > 0 {
		fileID = msg.Photo[len(msg.Photo)-1].FileID
		text = strings.TrimSpace(msg.Caption)
	}
	if fileID == "" && (text == "" || utf8.RuneCountInString(text) > 255) {
		b.send(tgID, "Пришли код текстом (до 255 символов) или скриншотом QR-кода.", nil)
		return nil
	}

	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}
	user, err := b.store.UserByTG(ctx, tgID)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(sess.Data["aid"], 10, 64)
	now := b.now()
	validUntil := b.policy.EndOfDay(now)
	ok, err := b.store.SetCode(ctx, id, user.ID, text, fileID, now, validUntil)
	if err != nil {
		return err
	}
	if !ok {
		return b.showMenu(ctx, tgID, "Не получилось сохранить код: подарок уже получен или закреплён не за тобой.")
	}
	a, err := b.store.Assignment(ctx, id)
	if err != nil {
		return err
	}
	b.sendCodeToRecipient(a)
	b.send(tgID, "Код отправлен получателю ✅ Если сегодня не заберут, я напомню прислать новый.", mainMenu())
	return nil
}

func (b *Bot) sendCodeToRecipient(a store.Assignment) {
	caption := fmt.Sprintf("🔑 Код получения посылки для <b>%s</b>", esc(a.ChildFirstName))
	if a.CodeText != "" {
		caption += fmt.Sprintf(":\n<code>%s</code>", esc(a.CodeText))
	}
	caption += fmt.Sprintf("\n\nПВЗ: %s\nКод действует <b>только сегодня</b>. Когда заберёшь, нажми «Забрал(а)».", esc(a.Parent.PVZAddress))
	markup := inlineButtons(button("✅ Забрал(а)", "got", a.ID), button("❌ Код не подошёл", "bad", a.ID))
	if a.CodeFileID != "" {
		b.sendPhoto(a.Parent.TgID, a.CodeFileID, caption, markup)
		return
	}
	b.send(a.Parent.TgID, caption, markup)
}

// ---------- recipient side ----------

func (b *Bot) showRecipientCards(ctx context.Context, user store.User) error {
	ex, ok, err := b.distributedExchange(ctx, user.TgID)
	if err != nil || !ok {
		return err
	}
	list, err := b.store.AssignmentsByParent(ctx, ex.ID, user.ID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		b.send(user.TgID, "Твоих детей нет в этом обмене.", nil)
		return nil
	}
	now := b.now()
	for _, a := range list {
		text := fmt.Sprintf("📦 Подарок для <b>%s</b>: %s", esc(a.ChildFirstName), b.statusText(a))
		var markup any
		if a.Status == domain.AssignmentArrived {
			if a.PickupState().CodeValid(now) {
				markup = inlineButtons(button("🔑 Показать код", "pick", a.ID), button("✅ Забрал(а)", "got", a.ID), button("❌ Код не подошёл", "bad", a.ID))
			} else {
				markup = inlineButtons(button("🚶 Иду забирать, нужен код", "pick", a.ID))
			}
		}
		b.send(user.TgID, text, markup)
	}
	return nil
}

func (b *Bot) recipientAssignment(ctx context.Context, user store.User, id int64) (store.Assignment, bool, error) {
	a, err := b.store.Assignment(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.Parent.ID != user.ID) {
		b.send(user.TgID, "Это не посылка для твоего ребёнка.", nil)
		return store.Assignment{}, false, nil
	}
	if err != nil {
		return store.Assignment{}, false, err
	}
	return a, true, nil
}

// requestCode asks the giver for a fresh code. invalidate=true means the recipient reported the current code did not work.
func (b *Bot) requestCode(ctx context.Context, user store.User, id int64, invalidate bool) error {
	a, ok, err := b.recipientAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	switch a.Status {
	case domain.AssignmentReceived:
		b.send(user.TgID, "Эта посылка уже отмечена как полученная 🎉", nil)
		return nil
	case domain.AssignmentAssigned, domain.AssignmentOrdered:
		b.send(user.TgID, "Посылка ещё не пришла в ПВЗ. Я напишу, когда даритель отметит доставку.", nil)
		return nil
	}

	now := b.now()
	if !invalidate && a.PickupState().CodeValid(now) {
		b.sendCodeToRecipient(a)
		return nil
	}
	ok, err = b.store.RequestCode(ctx, id, user.ID, invalidate, now)
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, "Не получилось запросить код, попробуй ещё раз.", nil)
		return nil
	}

	reason := "Получатель собирается забрать посылку"
	if invalidate {
		reason = "Код не подошёл"
	}
	b.send(a.Giver.TgID, fmt.Sprintf("🔔 %s для <b>%s</b>. Пришли, пожалуйста, сегодняшний код получения из приложения Wildberries.",
		reason, esc(a.ChildFirstName)), inlineButtons(button("🔑 Отправить код получения", "code", a.ID)))
	b.send(user.TgID, "Попросил дарителя прислать код. Как только он придёт, я перешлю его сюда.", nil)
	return nil
}

func (b *Bot) markReceived(ctx context.Context, user store.User, id int64) error {
	a, ok, err := b.recipientAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	ok, err = b.store.MarkReceived(ctx, id, user.ID, b.now())
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, "Эта посылка уже отмечена как полученная или ещё не отправлена.", nil)
		return nil
	}
	b.send(user.TgID, fmt.Sprintf("🎉 Ура! Подарок для %s получен. Спасибо!", esc(a.ChildFirstName)), nil)
	b.send(a.Giver.TgID, fmt.Sprintf("🎉 Подарок для <b>%s</b> получен. Спасибо, что участвуешь!", esc(a.ChildFirstName)), nil)
	return b.notifyIfAllReceived(ctx)
}

func (b *Bot) notifyIfAllReceived(ctx context.Context) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Status != domain.AssignmentReceived {
			return nil
		}
	}
	b.notifyAdmins("🎉 Все подарки получены! Можно завершить обмен командой /finish.")
	return nil
}

// parseDayMonth parses "DD.MM" and picks the year so the date is not far in the past (December orders may arrive in January).
func parseDayMonth(s string, now time.Time) (time.Time, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "/", ".")
	d, err := time.Parse("2.1", s)
	if err != nil {
		d, err = time.Parse("02.01", s)
		if err != nil {
			return time.Time{}, false
		}
	}
	date := time.Date(now.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	if date.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)) {
		date = date.AddDate(1, 0, 0)
	}
	return date, true
}
