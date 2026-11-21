package bot

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

const (
	stateChildFirst  = "child_first"
	stateChildLast   = "child_last"
	stateChildAge    = "child_age"
	stateChildGender = "child_gender"
	stateChildWishes = "child_wishes"
)

func (b *Bot) showChildren(ctx context.Context, user store.User) error {
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	children, err := b.store.ChildrenOf(ctx, ex.ID, user.ID)
	if err != nil {
		return err
	}
	open := ex.Status == domain.ExchangeOpen

	var sb strings.Builder
	sb.WriteString(b.tx("children.title") + "\n\n")
	if len(children) == 0 {
		sb.WriteString(b.tx("children.empty") + "\n")
	}
	var buttons []tgbotapi.InlineKeyboardButton
	for i, c := range children {
		sb.WriteString(b.tx("children.item", "n", strconv.Itoa(i+1), "child_full", c.FirstName+" "+c.LastName,
			"age", ageText(c.Age), "gender", genderText(c.Gender)) + "\n")
		if c.Wishes != "" {
			sb.WriteString(b.tx("children.wishes", "wishes", c.Wishes) + "\n")
		}
		if open {
			buttons = append(buttons, button(b.label("btn.delete_child", "child", c.FirstName), "delc", c.ID))
		}
	}
	if open {
		buttons = append(buttons, button(b.label("btn.add_child"), "addc", 0))
		sb.WriteString("\n" + b.tx("children.open_hint"))
	} else {
		sb.WriteString("\n" + b.tx("children.closed_hint"))
	}
	b.send(user.TgID, sb.String(), inlineButtons(buttons...))
	return nil
}

func (b *Bot) startChild(ctx context.Context, user store.User) error {
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeOpen {
		b.send(user.TgID, b.tx("children.add_closed"), nil)
		return nil
	}
	if user.BannedAt != nil {
		b.send(user.TgID, b.tx("children.banned"), nil)
		return nil
	}
	if err := b.store.SaveSession(ctx, user.TgID, store.Session{State: stateChildFirst, Data: map[string]string{}}); err != nil {
		return err
	}
	b.send(user.TgID, b.tx("children.ask_first"), b.cancelKeyboard())
	return nil
}

func (b *Bot) handleChildStep(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	switch sess.State {
	case stateChildFirst:
		if !validName(text) {
			b.send(tgID, b.tx("children.bad_first"), nil)
			return nil
		}
		sess.Data["first_name"] = text
		sess.State = stateChildLast
		b.send(tgID, b.tx("children.ask_last"), nil)

	case stateChildLast:
		if !validName(text) {
			b.send(tgID, b.tx("children.bad_last"), nil)
			return nil
		}
		sess.Data["last_name"] = text
		sess.State = stateChildAge
		b.send(tgID, b.tx("children.ask_age"), nil)

	case stateChildAge:
		age, err := strconv.Atoi(text)
		if err != nil || age < 0 || age > 17 {
			b.send(tgID, b.tx("children.bad_age"), nil)
			return nil
		}
		sess.Data["age"] = text
		sess.State = stateChildGender
		b.send(tgID, b.tx("children.ask_gender"), b.stepKeyboard(b.label("btn.boy"), b.label("btn.girl")))

	case stateChildGender:
		switch text {
		case b.label("btn.boy"):
			sess.Data["gender"] = "boy"
		case b.label("btn.girl"):
			sess.Data["gender"] = "girl"
		default:
			b.send(tgID, b.tx("children.bad_gender"), nil)
			return nil
		}
		sess.State = stateChildWishes
		b.send(tgID, b.tx("children.ask_wishes"), b.stepKeyboard(b.label("btn.skip")))

	case stateChildWishes:
		wishes := text
		if text == b.label("btn.skip") {
			wishes = ""
		}
		if utf8.RuneCountInString(wishes) > 1000 {
			b.send(tgID, b.tx("children.long_wishes"), nil)
			return nil
		}
		return b.saveChild(ctx, tgID, sess, wishes)
	}
	return b.store.SaveSession(ctx, tgID, sess)
}

func (b *Bot) saveChild(ctx context.Context, tgID int64, sess store.Session, wishes string) error {
	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}
	user, err := b.store.UserByTG(ctx, tgID)
	if err != nil {
		return err
	}
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeOpen {
		return b.showMenu(ctx, tgID, b.tx("children.closed_meanwhile"))
	}
	age, _ := strconv.Atoi(sess.Data["age"])
	err = b.store.AddChild(ctx, ex.ID, store.Child{
		ParentID:  user.ID,
		FirstName: sess.Data["first_name"],
		LastName:  sess.Data["last_name"],
		Age:       age,
		Gender:    sess.Data["gender"],
		Wishes:    wishes,
	})
	if err != nil {
		return err
	}
	b.send(tgID, b.tx("children.added"), b.mainMenu())
	return b.showChildren(ctx, user)
}

func (b *Bot) deleteChild(ctx context.Context, user store.User, childID int64) error {
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	ok, err := b.store.DeleteChild(ctx, ex.ID, user.ID, childID)
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, b.tx("children.delete_failed"), nil)
		return nil
	}
	return b.showChildren(ctx, user)
}
