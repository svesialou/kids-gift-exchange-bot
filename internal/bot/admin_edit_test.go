package bot

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

func TestEditParentUpdatesFamily(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, _ := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	if err := st.SaveUser(ctx, store.User{TgID: 100, TgUsername: "parent", FirstName: "Павел", LastName: "Старый", Phone: "+375291112233", PVZAddress: "Минск, старый адрес"}); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByTG(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}

	if err := b.EditParent(ctx, u.ID, " Анна ", " Петрова ", "+375 29 123-45-67", " Гомель, ул. Ленина 1 "); err != nil {
		t.Fatalf("valid edit must succeed: %v", err)
	}
	got, err := st.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := store.User{
		ID:         got.ID,
		TgID:       100,
		TgUsername: "parent",
		FirstName:  "Анна",
		LastName:   "Петрова",
		Phone:      "+375 29 123-45-67",
		PVZAddress: "Гомель, ул. Ленина 1",
		CreatedAt:  got.CreatedAt,
	}
	if got != want {
		t.Fatalf("edited user mismatch:\n got %+v\nwant %+v", got, want)
	}

	before, _ := st.UserByID(ctx, u.ID)
	var actionErr *ActionError
	if err := b.EditParent(ctx, u.ID, "  ", "Петрова", "abc", "Гомель"); !errors.As(err, &actionErr) {
		t.Fatalf("empty first name and bad phone must be refused, got %v", err)
	}
	if got, _ := st.UserByID(ctx, u.ID); got != before {
		t.Fatalf("refused edit must leave the row unchanged:\n got %+v\nwant %+v", got, before)
	}
	if err := b.EditParent(ctx, 999999, "Анна", "Петрова", "+375 29 123-45-67", "Гомель, ул. Ленина 1"); !errors.As(err, &actionErr) {
		t.Fatalf("editing a missing family must be refused, got %v", err)
	}
}

func TestEditChildUpdatesChild(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, _ := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatalf("exchange must be creatable: %v", err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, _ := st.ExchangeByID(ctx, id)
	if err := st.SaveUser(ctx, store.User{TgID: 200, FirstName: "Ольга", LastName: "Иванова", Phone: "+375291112233", PVZAddress: "Минск, ПВЗ 1"}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByTG(ctx, 200)
	if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Миша", LastName: "Иванов", Age: 5, Gender: "boy", Wishes: "мяч"}); err != nil {
		t.Fatal(err)
	}
	kids, err := st.ExchangeChildren(ctx, ex.ID)
	if err != nil || len(kids) != 1 {
		t.Fatalf("one child expected, got %d %v", len(kids), err)
	}
	kid := kids[0]

	if err := b.EditChild(ctx, kid.ID, " Маша ", " Иванова ", 7, " girl ", " кукла и конструктор "); err != nil {
		t.Fatalf("valid edit must succeed: %v", err)
	}
	got, err := st.ChildByID(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := store.ChildWithExchange{
		Child: store.Child{
			ID:        kid.ID,
			ParentID:  u.ID,
			FirstName: "Маша",
			LastName:  "Иванова",
			Age:       7,
			Gender:    "girl",
			Wishes:    "кукла и конструктор",
		},
		ExchangeID: ex.ID,
	}
	if got != want {
		t.Fatalf("edited child mismatch:\n got %+v\nwant %+v", got, want)
	}

	var actionErr *ActionError
	for _, tc := range []struct {
		name                string
		firstName, lastName string
		age                 int
		gender, wishes      string
	}{
		{"age out of range", "Маша", "Иванова", 20, "girl", "кукла"},
		{"bad gender", "Маша", "Иванова", 7, "alien", "кукла"},
		{"empty first name", "", "Иванова", 7, "girl", "кукла"},
	} {
		if err := b.EditChild(ctx, kid.ID, tc.firstName, tc.lastName, tc.age, tc.gender, tc.wishes); !errors.As(err, &actionErr) {
			t.Fatalf("%s: must be refused, got %v", tc.name, err)
		}
		if got, _ := st.ChildByID(ctx, kid.ID); got != want {
			t.Fatalf("%s: refused edit must leave the child unchanged:\n got %+v\nwant %+v", tc.name, got, want)
		}
	}
	if err := b.EditChild(ctx, 999999, "Маша", "Иванова", 7, "girl", "кукла"); !errors.As(err, &actionErr) {
		t.Fatalf("editing a missing child must be refused, got %v", err)
	}
}

func TestDeleteChildSoftDeletes(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, _ := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatalf("exchange must be creatable: %v", err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, _ := st.ExchangeByID(ctx, id)
	if err := st.SaveUser(ctx, store.User{TgID: 200, FirstName: "Ольга", LastName: "Иванова", Phone: "+375291112233", PVZAddress: "Минск, ПВЗ 1"}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByTG(ctx, 200)
	if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Миша", LastName: "Иванов", Age: 5, Gender: "boy"}); err != nil {
		t.Fatal(err)
	}
	kids, _ := st.ExchangeChildren(ctx, ex.ID)

	if err := b.DeleteChild(ctx, kids[0].ID); err != nil {
		t.Fatalf("delete must succeed: %v", err)
	}
	if _, err := st.ChildByID(ctx, kids[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted child must be gone, got %v", err)
	}

	var actionErr *ActionError
	if err := b.DeleteChild(ctx, kids[0].ID); !errors.As(err, &actionErr) {
		t.Fatalf("deleting a missing child must be refused, got %v", err)
	}
	if err := b.DeleteChild(ctx, 999999); !errors.As(err, &actionErr) {
		t.Fatalf("deleting an unknown child must be refused, got %v", err)
	}
}

func TestDeleteChildBlockedWhenAssigned(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, _ := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatalf("exchange must be creatable: %v", err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, _ := st.ExchangeByID(ctx, id)
	for i, tgID := range []int64{300, 301} {
		if err := st.SaveUser(ctx, store.User{TgID: tgID, FirstName: "Родитель", LastName: "Семья", Phone: "+375291112233", PVZAddress: "Минск, ПВЗ 1"}); err != nil {
			t.Fatal(err)
		}
		u, _ := st.UserByTG(ctx, tgID)
		gender := "boy"
		if i == 1 {
			gender = "girl"
		}
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Ребёнок", LastName: "Семья", Age: 6, Gender: gender}); err != nil {
			t.Fatal(err)
		}
	}
	kids, err := st.ExchangeChildren(ctx, ex.ID)
	if err != nil || len(kids) != 2 {
		t.Fatalf("two children expected, got %d %v", len(kids), err)
	}

	if _, err := b.ChangeExchangeStatus(ctx, id, "close"); err != nil {
		t.Fatal(err)
	}
	n, err := b.Distribute(ctx, id)
	if err != nil || n != 2 {
		t.Fatalf("two families must distribute into 2 assignments, got %d %v", n, err)
	}

	var actionErr *ActionError
	if err := b.DeleteChild(ctx, kids[0].ID); !errors.As(err, &actionErr) || actionErr.Msg != "Подарок для этого ребёнка уже распределён — удалить нельзя." {
		t.Fatalf("assigned child must be refused with the assignment message, got %v", err)
	}
	if got, getErr := st.ChildByID(ctx, kids[0].ID); getErr != nil || got.ID != kids[0].ID {
		t.Fatalf("assigned child must stay, got %+v %v", got, getErr)
	}
	if err := b.DeleteChild(ctx, kids[0].ID+1000); !errors.As(err, &actionErr) || actionErr.Msg != "Ребёнок не найден." {
		t.Fatalf("unknown child must be refused with the missing message, got %v", err)
	}
}

func TestEditParentPVZChangeNotifiesGivers(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, tg := newFakeTelegram(t)
	b := New(api, st, config.Config{Location: time.UTC, AdminIDs: []int64{900}})
	clock := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return clock }

	id, err := b.NewExchange(ctx, testSettings("Новый год"))
	if err != nil {
		t.Fatalf("exchange must be creatable: %v", err)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatal(err)
	}
	ex, _ := st.ExchangeByID(ctx, id)
	var parent, giver store.User
	for i, tgID := range []int64{300, 301} {
		if err := st.SaveUser(ctx, store.User{TgID: tgID, FirstName: "Родитель", LastName: "Семья", Phone: "+375 29 111-22-33", PVZAddress: "Минск, ПВЗ 1"}); err != nil {
			t.Fatal(err)
		}
		u, _ := st.UserByTG(ctx, tgID)
		gender := "boy"
		if i == 1 {
			gender = "girl"
		}
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Ребёнок", LastName: "Семья", Age: 6, Gender: gender}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			parent = u
		} else {
			giver = u
		}
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "close"); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Distribute(ctx, id); err != nil || n != 2 {
		t.Fatalf("two families must distribute into 2 assignments, got %d %v", n, err)
	}

	beforeGiver := tg.textsTo(fmt.Sprint(giver.TgID))
	beforeAdmin := tg.textsTo("900")
	joined := func(before, msg string) string {
		if before == "" {
			return msg
		}
		return before + "\n---\n" + msg
	}
	if err := b.EditParent(ctx, parent.ID, "Родитель", "Семья", "+375 29 111-22-33", " Гомель, новый ПВЗ "); err != nil {
		t.Fatalf("pvz edit must succeed: %v", err)
	}
	wantGiver := "⚠️ У получателя подарка для <b>Ребёнок</b> изменился адрес ПВЗ:\nГомель, новый ПВЗ\n\nЕсли ещё не заказал(а), заказывай на новый адрес."
	if got := tg.textsTo(fmt.Sprint(giver.TgID)); got != joined(beforeGiver, wantGiver) {
		t.Fatalf("giver notification mismatch:\n got %q\nwant %q", got, joined(beforeGiver, wantGiver))
	}
	wantAdmin := fmt.Sprintf("Участник (user %d) сменил адрес ПВЗ после распределения.", parent.ID)
	if got := tg.textsTo("900"); got != joined(beforeAdmin, wantAdmin) {
		t.Fatalf("admin notification mismatch:\n got %q\nwant %q", got, joined(beforeAdmin, wantAdmin))
	}

	if err := b.EditParent(ctx, parent.ID, "Родитель", "Семья", "+375 29 444-55-66", "Гомель, новый ПВЗ"); err != nil {
		t.Fatalf("phone edit must succeed: %v", err)
	}
	if got := tg.textsTo(fmt.Sprint(giver.TgID)); got != joined(beforeGiver, wantGiver) {
		t.Fatalf("phone edit must not notify the giver, got %q", got)
	}
	if got := tg.textsTo("900"); got != joined(beforeAdmin, wantAdmin) {
		t.Fatalf("phone edit must not notify admins, got %q", got)
	}
}
