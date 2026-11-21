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
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}, GiftBudgetMax: 25, GiftCurrency: "BYN"})

	if ex, _ := st.CurrentExchange(ctx); ex.ID != 0 || ex.Status != domain.ExchangeNone {
		t.Fatalf("empty database must have no exchange, got %+v", ex)
	}
	day := func(d int) time.Time { return time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC) }
	if err := b.NewExchange(ctx, Settings{Title: "Новый год", BudgetMax: 25, Currency: "BYN", OrderDeadline: day(13), DeliveryBy: day(25)}); err != nil {
		t.Fatalf("first exchange must be creatable: %v", err)
	}
	ex, _ := st.CurrentExchange(ctx)
	if _, err := b.ChangeExchangeStatus(ctx, "open"); err != nil {
		t.Fatal(err)
	}

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

	n, err := b.SendMessage(ctx, []int64{ids[0], ids[2], 999999}, "Привет")
	if err != nil || n != 2 {
		t.Fatalf("message must reach the 2 existing users, got %d %v", n, err)
	}
	if !strings.Contains(tg.textsTo("100"), "Привет") || tg.textsTo("101") != "" || !strings.Contains(tg.textsTo("102"), "Привет") {
		t.Fatal("message must go only to the selected users")
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

	if err := b.NewExchange(ctx, Settings{Title: "Ещё", BudgetMax: 25, Currency: "BYN", OrderDeadline: day(13), DeliveryBy: day(25)}); err == nil {
		t.Fatal("a second exchange must not start while the first one runs")
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
	if cur, _ := st.CurrentExchange(ctx); cur.ID != 0 {
		t.Fatalf("after deleting the only exchange there must be none, got %+v", cur)
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
