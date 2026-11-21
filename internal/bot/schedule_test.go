package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

// fakeTelegram answers every Bot API call with success and remembers sent texts per chat.
type fakeTelegram struct {
	mu   sync.Mutex
	sent map[string][]string
}

func newFakeTelegram(t *testing.T) (*tgbotapi.BotAPI, *fakeTelegram) {
	t.Helper()
	ft := &fakeTelegram{sent: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			ft.mu.Lock()
			ft.sent[r.Form.Get("chat_id")] = append(ft.sent[r.Form.Get("chat_id")], r.Form.Get("text"))
			ft.mu.Unlock()
		}
		result := json.RawMessage(`{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}`)
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			result = json.RawMessage(`{"id":1,"is_bot":true,"first_name":"test","username":"test_bot"}`)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	api, err := tgbotapi.NewBotAPIWithClient("1:test", srv.URL+"/bot%s/%s", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return api, ft
}

func (f *fakeTelegram) textsTo(chatID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.sent[chatID], "\n---\n")
}

func TestScheduleDrivesExchange(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	api, tg := newFakeTelegram(t)
	minsk, _ := time.LoadLocation("Europe/Minsk")
	cfg := config.Config{Location: minsk, AdminIDs: []int64{900},
		MorningHour: 10, EveningHour: 19, QuietHour: 22, StuckAfter: 96 * time.Hour}
	b := New(api, st, cfg)

	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, minsk)
	b.now = func() time.Time { return clock }
	date := func(d int) time.Time { return time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC) }
	opens := time.Date(2026, 11, 25, 10, 0, 0, 0, minsk)
	closes := time.Date(2026, 12, 5, 20, 0, 0, 0, minsk)
	id, err := b.NewExchange(ctx, Settings{Title: "Новый год", BudgetMax: 30, Currency: "BYN",
		RegistrationOpensAt: &opens, RegistrationClosesAt: &closes, OrderDeadline: date(13), DeliveryBy: date(25)})
	if err != nil {
		t.Fatal(err)
	}
	status := func() domain.ExchangeStatus {
		ex, err := st.ExchangeByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ex.Status
	}
	tick := func(at time.Time) {
		t.Helper()
		clock = at
		if err := b.tick(ctx); err != nil {
			t.Fatal(err)
		}
	}

	clock = opens.Add(-time.Minute)
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err == nil {
		t.Fatal("registration must not open before its window")
	}
	tick(opens.Add(time.Hour))
	if s := status(); s != domain.ExchangeDraft {
		t.Fatalf("the scheduler never opens registration by itself, got %s", s)
	}
	if _, err := b.ChangeExchangeStatus(ctx, id, "open"); err != nil {
		t.Fatalf("registration opens by hand inside its window: %v", err)
	}

	ex, _ := st.ExchangeByID(ctx, id)
	for i, kids := range []int{2, 1, 1} {
		tgID := int64(100 + i)
		if err := st.SaveUser(ctx, store.User{TgID: tgID, FirstName: "P", LastName: "L", Phone: "+375291234567", PVZAddress: "Минск"}); err != nil {
			t.Fatal(err)
		}
		u, _ := st.UserByTG(ctx, tgID)
		for k := 0; k < kids; k++ {
			_ = st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "girl"})
		}
	}

	tick(closes)
	if s := status(); s != domain.ExchangeDistributed {
		t.Fatalf("registration close must trigger distribution, got %s", s)
	}
	if !strings.Contains(tg.textsTo("100"), "Распределение готово") {
		t.Fatalf("giver did not get the distribution message: %q", tg.textsTo("100"))
	}
	if !strings.Contains(tg.textsTo("100"), "до 30 BYN") {
		t.Fatalf("cards must use the exchange budget: %q", tg.textsTo("100"))
	}

	list, _ := st.AssignmentsByExchange(ctx, ex.ID)
	for _, a := range list[:2] {
		if ok, err := st.MarkOrdered(ctx, a.ID, a.Giver.ID, nil, clock); err != nil || !ok {
			t.Fatalf("order: %v %v", ok, err)
		}
	}
	if ok, err := st.MarkArrived(ctx, list[0].ID, list[0].Giver.ID, clock); err != nil || !ok {
		t.Fatalf("arrive: %v %v", ok, err)
	}

	tick(time.Date(2026, 12, 13, 23, 59, 0, 0, minsk))
	if vs, _ := st.ViolationsByExchange(ctx, ex.ID); len(vs) != 0 {
		t.Fatalf("no violations before the order deadline ends, got %d", len(vs))
	}
	tick(time.Date(2026, 12, 14, 10, 0, 0, 0, minsk))
	vs, _ := st.ViolationsByExchange(ctx, ex.ID)
	if len(vs) != 2 {
		t.Fatalf("want 2 order violations after the deadline, got %d", len(vs))
	}
	if !strings.Contains(tg.textsTo("900"), "срок отправки подарков прошёл") {
		t.Fatalf("admins must hear about the deadline: %q", tg.textsTo("900"))
	}

	tick(time.Date(2026, 12, 26, 10, 0, 0, 0, minsk))
	if s := status(); s != domain.ExchangeCompleted {
		t.Fatalf("after the end date the exchange completes, got %s", s)
	}
	vs, _ = st.ViolationsByExchange(ctx, ex.ID)
	delivery := 0
	for _, v := range vs {
		if v.Kind == domain.ViolationDeliveryOverdue {
			delivery++
		}
	}
	if delivery != 3 {
		t.Fatalf("want 3 gifts not delivered by the end date, got %d", delivery)
	}
}
