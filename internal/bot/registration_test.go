package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

func startMessage(tgID int64, payload string) *tgbotapi.Message {
	text := "/start"
	if payload != "" {
		text += " " + payload
	}
	return &tgbotapi.Message{
		From:     &tgbotapi.User{ID: tgID},
		Text:     text,
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
	}
}

func TestOnStartRegistersOnlyWithExchangeInviteLink(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, tg := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := b.NewExchange(ctx, testSettings("Ещё один"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, err := st.ExchangeByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.ExchangeByID(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if other.InviteCode == ex.InviteCode {
		t.Fatal("every exchange must have its own invite code")
	}

	const tgID = 200
	if err := b.handleMessage(ctx, startMessage(tgID, "")); err != nil {
		t.Fatal(err)
	}
	if err := b.handleMessage(ctx, startMessage(tgID, other.InviteCode)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tg.textsTo("200"), "открой бота по ссылке-приглашению") {
		t.Fatalf("wrong or missing invite code must not start registration: %q", tg.textsTo("200"))
	}

	if err := b.handleMessage(ctx, startMessage(tgID, ex.InviteCode)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tg.textsTo("200"), "Сначала зарегистрируемся") {
		t.Fatalf("the exchange's own invite code must start registration: %q", tg.textsTo("200"))
	}
	sess, err := st.Session(ctx, tgID)
	if err != nil || sess.State != stateRegFirst {
		t.Fatalf("registration session must start, got %+v %v", sess, err)
	}
}

func completeRegistration(t *testing.T, b *Bot, ctx context.Context, tgID int64) {
	t.Helper()
	msg := func(text string) *tgbotapi.Message {
		return &tgbotapi.Message{From: &tgbotapi.User{ID: tgID}, Text: text}
	}
	steps := []string{"Анна", "Иванова", "+375291234567", "Минск, ул. Ленина 1"}
	for _, s := range steps {
		if err := b.handleMessage(ctx, msg(s)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRegistrationAsksIfUserJoinsAsParticipant(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, tg := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, err := st.ExchangeByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	answer := func(tgID int64, text string) {
		t.Helper()
		if err := b.handleMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: tgID}, Text: text}); err != nil {
			t.Fatal(err)
		}
	}

	const yesID, noID = 200, 201
	for _, tgID := range []int64{yesID, noID} {
		if err := b.handleMessage(ctx, startMessage(tgID, ex.InviteCode)); err != nil {
			t.Fatal(err)
		}
		completeRegistration(t, b, ctx, tgID)
		sess, err := st.Session(ctx, tgID)
		if err != nil || sess.State != stateRegSelf {
			t.Fatalf("after registration bot must ask about self participation, got %+v %v", sess, err)
		}
		if !strings.Contains(tg.textsTo(fmt.Sprint(tgID)), b.tx("reg.ask_self")) {
			t.Fatalf("self participation question must be sent: %q", tg.textsTo(fmt.Sprint(tgID)))
		}
	}

	user, err := st.UserByTG(ctx, yesID)
	if err != nil {
		t.Fatal(err)
	}
	answer(yesID, b.label("btn.self_yes"))
	sess, err := st.Session(ctx, yesID)
	if err != nil || sess.State != stateChildAge || sess.Data["first_name"] != "Анна" {
		t.Fatalf("«Да» must continue as participant with registration name prefilled, got %+v %v", sess, err)
	}
	answer(yesID, "30")
	answer(yesID, b.label("btn.female"))
	answer(yesID, b.label("btn.skip"))
	children, err := st.ChildrenOf(ctx, ex.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].FirstName != "Анна" || children[0].LastName != "Иванова" ||
		children[0].Age != 30 || children[0].Gender != "female" || children[0].Wishes != "" {
		t.Fatalf("answering yes must add the user as a participant, got %+v", children)
	}
	if sess, err = st.Session(ctx, yesID); err != nil || sess.State != "" {
		t.Fatalf("participant flow must clear the session, got %+v %v", sess, err)
	}

	answer(noID, b.label("btn.self_no"))
	noUser, err := st.UserByTG(ctx, noID)
	if err != nil {
		t.Fatal(err)
	}
	if children, err = st.ChildrenOf(ctx, ex.ID, noUser.ID); err != nil || len(children) != 0 {
		t.Fatalf("answering no must not add a participant, got %+v %v", children, err)
	}
	if sess, err = st.Session(ctx, noID); err != nil || sess.State != "" {
		t.Fatalf("answering no must clear the session, got %+v %v", sess, err)
	}
}
