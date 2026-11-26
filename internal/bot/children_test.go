package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

func TestValidAgeBoundaries(t *testing.T) {
	for _, age := range []int{0, 17, 30, 99} {
		if !validAge(age) {
			t.Errorf("validAge(%d) = false, want true", age)
		}
	}
	for _, age := range []int{-1, 100, 150} {
		if validAge(age) {
			t.Errorf("validAge(%d) = true, want false", age)
		}
	}
}

func TestGenderText(t *testing.T) {
	tests := map[string]string{
		"boy":     "мальчик",
		"girl":    "девочка",
		"male":    "мужчина",
		"female":  "женщина",
		"unknown": "мальчик",
	}
	for g, want := range tests {
		if got := genderText(g); got != want {
			t.Errorf("genderText(%q) = %q, want %q", g, got, want)
		}
	}
}

func TestValidateChildAdult(t *testing.T) {
	if err := validateChild("Иван", "Иванов", 30, "male", ""); err != nil {
		t.Fatalf("adult participant must pass: %v", err)
	}
	if err := validateChild("Иван", "Иванов", 30, "female", "книга"); err != nil {
		t.Fatalf("adult participant with wishes must pass: %v", err)
	}
	err := validateChild("Иван", "Иванов", 100, "male", "")
	var actionErr *ActionError
	if !errors.As(err, &actionErr) {
		t.Fatalf("age 100 must be refused, got %v", err)
	}
	if actionErr.Msg != "Возраст: от 0 до 99." {
		t.Fatalf("age 100 refusal = %q, want %q", actionErr.Msg, "Возраст: от 0 до 99.")
	}
}

func TestAdultParticipantFlowStoresGender(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, tg := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})

	exID, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, exID, "open"); err != nil {
		t.Fatal(err)
	}

	const tgID = 300
	msg := func(text string) *tgbotapi.Message {
		return &tgbotapi.Message{From: &tgbotapi.User{ID: tgID}, Text: text}
	}
	sess := store.Session{State: stateChildAge, Data: map[string]string{"first_name": "Иван", "last_name": "Иванов"}}
	if err := b.handleChildStep(ctx, msg("30"), sess); err != nil {
		t.Fatal(err)
	}
	if got, want := tg.textsTo("300"), b.tx("children.ask_gender_adult"); got != want {
		t.Fatalf("adult age step must ask about adult gender: got %q, want %q", got, want)
	}
	sess, err = st.Session(ctx, tgID)
	if err != nil || sess.State != stateChildGender {
		t.Fatalf("session must move to the gender step, got %+v %v", sess, err)
	}

	if err := b.handleChildStep(ctx, msg(b.label("btn.male")), sess); err != nil {
		t.Fatal(err)
	}
	sess, err = st.Session(ctx, tgID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.State != stateChildWishes || sess.Data["gender"] != "male" {
		t.Fatalf("«Мужчина» must store gender \"male\", got %+v", sess)
	}
}
