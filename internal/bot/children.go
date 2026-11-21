package bot

import (
	"context"
	"fmt"
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
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	children, err := b.store.ChildrenOf(ctx, ex.ID, user.ID)
	if err != nil {
		return err
	}
	open := ex.Status == domain.ExchangeOpen

	var sb strings.Builder
	sb.WriteString("<b>Мои дети</b>\n\n")
	if len(children) == 0 {
		sb.WriteString("Пока никого не добавлено.\n")
	}
	var buttons []tgbotapi.InlineKeyboardButton
	for i, c := range children {
		fmt.Fprintf(&sb, "%d. %s %s, %s, %s\n", i+1, esc(c.FirstName), esc(c.LastName), ageText(c.Age), genderText(c.Gender))
		if c.Wishes != "" {
			fmt.Fprintf(&sb, "   Пожелания: %s\n", esc(c.Wishes))
		}
		if open {
			buttons = append(buttons, button("❌ Удалить: "+c.FirstName, "delc", c.ID))
		}
	}
	if open {
		buttons = append(buttons, button("➕ Добавить ребёнка", "addc", 0))
		sb.WriteString("\nПока регистрация открыта, список можно менять. Чтобы исправить данные ребёнка, удали его и добавь заново.")
	} else {
		sb.WriteString("\nРегистрация закрыта, список детей изменить нельзя.")
	}
	b.send(user.TgID, sb.String(), inlineButtons(buttons...))
	return nil
}

func (b *Bot) startChild(ctx context.Context, user store.User) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeOpen {
		b.send(user.TgID, "Регистрация закрыта, добавить ребёнка уже нельзя.", nil)
		return nil
	}
	if err := b.store.SaveSession(ctx, user.TgID, store.Session{State: stateChildFirst, Data: map[string]string{}}); err != nil {
		return err
	}
	b.send(user.TgID, "Как зовут ребёнка? Напиши <b>имя</b>.", cancelKeyboard())
	return nil
}

func (b *Bot) handleChildStep(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	switch sess.State {
	case stateChildFirst:
		if !validName(text) {
			b.send(tgID, "Имя должно быть от 1 до 100 символов.", nil)
			return nil
		}
		sess.Data["first_name"] = text
		sess.State = stateChildLast
		b.send(tgID, "<b>Фамилия</b> ребёнка?", nil)

	case stateChildLast:
		if !validName(text) {
			b.send(tgID, "Фамилия должна быть от 1 до 100 символов.", nil)
			return nil
		}
		sess.Data["last_name"] = text
		sess.State = stateChildAge
		b.send(tgID, "Сколько полных лет? Напиши число.", nil)

	case stateChildAge:
		age, err := strconv.Atoi(text)
		if err != nil || age < 0 || age > 17 {
			b.send(tgID, "Напиши возраст числом от 0 до 17.", nil)
			return nil
		}
		sess.Data["age"] = text
		sess.State = stateChildGender
		b.send(tgID, "Мальчик или девочка?", stepKeyboard(btnBoy, btnGirl))

	case stateChildGender:
		switch text {
		case btnBoy:
			sess.Data["gender"] = "boy"
		case btnGirl:
			sess.Data["gender"] = "girl"
		default:
			b.send(tgID, "Выбери кнопкой: мальчик или девочка.", nil)
			return nil
		}
		sess.State = stateChildWishes
		b.send(tgID, "Что ребёнок любит или о чём мечтает? Это поможет дарителю выбрать подарок. Можно пропустить.", stepKeyboard(btnSkip))

	case stateChildWishes:
		wishes := text
		if text == btnSkip {
			wishes = ""
		}
		if utf8.RuneCountInString(wishes) > 1000 {
			b.send(tgID, "Слишком длинно, сократи до 1000 символов.", nil)
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
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeOpen {
		return b.showMenu(ctx, tgID, "Регистрация закрылась, ребёнка добавить не получилось.")
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
	b.send(tgID, "Ребёнок добавлен ✅", mainMenu())
	return b.showChildren(ctx, user)
}

func (b *Bot) deleteChild(ctx context.Context, user store.User, childID int64) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	ok, err := b.store.DeleteChild(ctx, ex.ID, user.ID, childID)
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, "Удалить не получилось: регистрация уже закрыта или ребёнок не найден.", nil)
		return nil
	}
	return b.showChildren(ctx, user)
}
