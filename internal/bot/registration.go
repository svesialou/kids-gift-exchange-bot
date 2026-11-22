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
		return b.showMenu(ctx, tgID, b.tx("reg.welcome_back", "name", user.FirstName))
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeOpen {
		b.send(tgID, b.tx("reg.closed"), tgbotapi.NewRemoveKeyboard(true))
		return nil
	}
	if ex.InviteCode != "" && payload != ex.InviteCode && !b.cfg.IsAdmin(tgID) {
		b.send(tgID, b.tx("reg.need_invite"), tgbotapi.NewRemoveKeyboard(true))
		return nil
	}

	b.send(tgID, b.tx("reg.intro"), nil)
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
	b.send(tgID, b.tx("reg.ask_first"), b.cancelKeyboard())
	return nil
}

func (b *Bot) handleRegistrationStep(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	switch sess.State {
	case stateRegFirst:
		if !validName(text) {
			b.send(tgID, b.tx("reg.bad_first"), nil)
			return nil
		}
		sess.Data["first_name"] = text
		sess.State = stateRegLast
		b.send(tgID, b.tx("reg.ask_last"), nil)

	case stateRegLast:
		if !validName(text) {
			b.send(tgID, b.tx("reg.bad_last"), nil)
			return nil
		}
		sess.Data["last_name"] = text
		sess.State = stateRegPhone
		kb := tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButtonContact(b.label("btn.share_phone"))),
			tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(b.label("btn.cancel"))),
		)
		kb.ResizeKeyboard = true
		b.send(tgID, b.tx("reg.ask_phone"), kb)

	case stateRegPhone:
		phone := text
		if msg.Contact != nil {
			if msg.Contact.UserID != 0 && msg.Contact.UserID != tgID {
				b.send(tgID, b.tx("reg.foreign_contact"), nil)
				return nil
			}
			phone = msg.Contact.PhoneNumber
		}
		if !validPhone(phone) {
			b.send(tgID, b.tx("reg.bad_phone"), nil)
			return nil
		}
		sess.Data["phone"] = phone
		sess.State = stateRegPVZ
		b.send(tgID, b.tx("reg.ask_pvz"), b.cancelKeyboard())

	case stateRegPVZ:
		if utf8.RuneCountInString(text) < 5 || utf8.RuneCountInString(text) > 500 {
			b.send(tgID, b.tx("reg.bad_pvz"), nil)
			return nil
		}
		return b.finishRegistration(ctx, msg, sess, text)
	}
	return b.store.SaveSession(ctx, tgID, sess)
}

func (b *Bot) finishRegistration(ctx context.Context, msg *tgbotapi.Message, sess store.Session, pvz string) error {
	tgID := msg.From.ID
	editing := sess.Data["edit"] == "1"

	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	if !editing && ex.Status != domain.ExchangeOpen {
		if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
			return err
		}
		b.send(tgID, b.tx("reg.closed_meanwhile"), tgbotapi.NewRemoveKeyboard(true))
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
		return b.showMenu(ctx, tgID, b.tx("reg.updated"))
	}

	b.send(tgID, b.tx("reg.done"), b.mainMenu())
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
		b.send(a.Giver.TgID, b.tx("reg.pvz_changed", "child", a.ChildFirstName, "pvz", pvz), nil)
	}
	b.notifyAdmins(fmt.Sprintf("Участник (user %d) сменил адрес ПВЗ после распределения.", parentID))
}

func (b *Bot) showProfile(ctx context.Context, user store.User) error {
	text := b.tx("reg.profile", "first_name", user.FirstName, "last_name", user.LastName, "phone", user.Phone, "pvz", user.PVZAddress)
	b.send(user.TgID, text, inlineButtons(button(b.label("btn.edit_profile"), "prof", 0)))
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
