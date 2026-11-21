package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

func TestDeleteExchangeMessagesAndBans(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, tg := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	if ex, _ := st.ActiveExchange(ctx); ex.ID != 0 || ex.Status != domain.ExchangeNone {
		t.Fatalf("empty database must have no exchange, got %+v", ex)
	}
	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatalf("first exchange must be creatable: %v", err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, _ := st.ExchangeByID(ctx, id)

	var ids []int64
	for i := 0; i < 3; i++ {
		tgID := int64(100 + i)
		_ = st.SaveUser(ctx, store.User{TgID: tgID, FirstName: "P", LastName: "L", Phone: "+375291234567", PVZAddress: "Минск"})
		u, _ := st.UserByTG(ctx, tgID)
		ids = append(ids, u.ID)
		if i < 2 {
			_ = st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "boy"})
		}
	}

	if err := b.Texts().Set(ctx, "org.message", "Записка от оргкомитета: {text}"); err != nil {
		t.Fatal(err)
	}
	n, err := b.SendMessage(ctx, []int64{ids[0], ids[2], 999999}, "Привет")
	if err != nil || n != 2 {
		t.Fatalf("message must reach the 2 existing users, got %d %v", n, err)
	}
	if !strings.Contains(tg.textsTo("100"), "Привет") || tg.textsTo("101") != "" || !strings.Contains(tg.textsTo("102"), "Привет") {
		t.Fatal("message must go only to the selected users")
	}
	if !strings.Contains(tg.textsTo("100"), "Записка от оргкомитета: Привет") {
		t.Fatalf("organizer-edited text must reach the chat: %q", tg.textsTo("100"))
	}

	if n, err := b.SetBanned(ctx, ids[:2], true, "тест"); err != nil || n != 2 {
		t.Fatalf("bulk ban: %d %v", n, err)
	}
	if _, err := b.SetBanned(ctx, ids[:1], true, " "); err == nil {
		t.Fatal("ban without a reason must be refused")
	}
	if n, err := b.SetBanned(ctx, ids[:2], false, ""); err != nil || n != 2 {
		t.Fatalf("bulk unban: %d %v", n, err)
	}

	secondID, err := b.NewExchange(ctx, testSettings("Ещё один"))
	if err != nil {
		t.Fatalf("a second exchange can be planned while the first runs: %v", err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, secondID, "open"); err == nil {
		t.Fatal("a second exchange must not open while the first one runs")
	}

	if err := b.DeleteExchange(ctx, ex.ID, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tg.textsTo("100"), "отменён") || !strings.Contains(tg.textsTo("101"), "отменён") {
		t.Fatal("parents of a running exchange must hear it was cancelled")
	}
	if strings.Contains(tg.textsTo("102"), "отменён") {
		t.Fatal("users without children in the exchange must not be notified")
	}
	if cur, _ := st.ActiveExchange(ctx); cur.ID != 0 {
		t.Fatalf("after deleting the running exchange none is active, got %+v", cur)
	}
	if _, err := b.ChangeExchangeStatus(ctx, secondID, "open"); err != nil {
		t.Fatalf("the planned exchange can open once the running one is gone: %v", err)
	}
	if kids, _ := st.ExchangeChildren(ctx, ex.ID); len(kids) != 0 {
		t.Fatalf("children of a deleted exchange must be gone, got %d", len(kids))
	}
	if u, err := st.UserByID(ctx, ids[0]); err != nil || u.ID == 0 {
		t.Fatalf("adult profiles survive exchange deletion: %v", err)
	}
	if err := b.DeleteExchange(ctx, ex.ID, false); err == nil {
		t.Fatal("deleting a missing exchange must be refused")
	}
}

// testSettings has a registration window around 2026-11-26 12:00 UTC.
func testSettings(title string) Settings {
	opens := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	closes := time.Date(2026, 12, 5, 20, 0, 0, 0, time.UTC)
	return Settings{Title: title, BudgetMax: 25, Currency: "BYN", RegistrationOpensAt: &opens, RegistrationClosesAt: &closes,
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryBy: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)}
}
