package bot

import (
	"context"
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
