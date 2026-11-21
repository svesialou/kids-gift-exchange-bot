package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/bot"
	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

type fakeAdmin struct{ actions []string }

func (f *fakeAdmin) ChangeExchangeStatus(_ context.Context, _ int64, action string) (domain.ExchangeStatus, error) {
	f.actions = append(f.actions, action)
	return "", &bot.ActionError{Msg: "Нельзя: сейчас «черновик»."}
}
func (f *fakeAdmin) NewExchange(context.Context, bot.Settings) (int64, error) { return 1, nil }
func (f *fakeAdmin) PreviewDistribution(context.Context, int64) (bot.DistributionPreview, error) {
	return bot.DistributionPreview{Families: 1, Children: 2}, nil
}
func (f *fakeAdmin) Distribute(context.Context, int64) (int, error) { return 0, nil }
func (f *fakeAdmin) RemindNow(context.Context) (int, error)         { return 0, nil }
func (f *fakeAdmin) Reassign(context.Context, int64, int64) error   { return nil }
func (f *fakeAdmin) Broadcast(context.Context, string) (int, error) { return 0, nil }
func (f *fakeAdmin) EffectiveSettings(ex store.Exchange) bot.Settings {
	return bot.Settings{Title: "Обмен", BudgetMax: 25, Currency: "BYN"}
}
func (f *fakeAdmin) SetBanned(context.Context, []int64, bool, string) (int, error) { return 0, nil }
func (f *fakeAdmin) SendMessage(context.Context, []int64, string) (int, error)     { return 0, nil }
func (f *fakeAdmin) DeleteExchange(context.Context, int64, bool) error             { return nil }
func (f *fakeAdmin) AddViolation(context.Context, int64, int64, string) error      { return nil }
func (f *fakeAdmin) SetViolationDismissed(context.Context, int64, bool) error      { return nil }
func (f *fakeAdmin) UpdateSettings(context.Context, int64, bot.Settings) error     { return nil }
func (f *fakeAdmin) InviteLink() string                                            { return "https://t.me/test_bot?start=x" }

func testStore(t *testing.T) *store.Store { return storetest.New(t) }

func TestPanelAccessAndState(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	exID, err := st.CreateExchange(ctx, store.ExchangeSchedule{Title: "Новый год", BudgetMax: 25, Currency: "BYN",
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionExchange(ctx, exID, domain.ExchangeOpen, domain.ExchangeDraft); err != nil {
		t.Fatal(err)
	}
	ex, _ := st.ExchangeByID(ctx, exID)
	if err := st.SaveUser(ctx, store.User{TgID: 1001, FirstName: "Анна", LastName: "Петрова", Phone: "+375291112233", PVZAddress: "Минск, Победителей 1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUser(ctx, store.User{TgID: 1002, FirstName: "Без", LastName: "Детей", Phone: "+375291112244", PVZAddress: "Минск"}); err != nil {
		t.Fatal(err)
	}
	anna, _ := st.UserByTG(ctx, 1001)
	if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: anna.ID, FirstName: "Маша", LastName: "Петрова", Age: 6, Gender: "girl"}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Location: time.UTC, TelegramToken: testToken, AdminIDs: []int64{42}, StuckAfter: 96 * time.Hour}
	admin := &fakeAdmin{}
	srv := httptest.NewServer(New(cfg, st, admin).Handler())
	defer srv.Close()

	initData := func(userID int64) string {
		return signInitData(t, testToken, map[string]string{
			"auth_date": strconv.FormatInt(time.Now().Unix(), 10),
			"user":      fmt.Sprintf(`{"id":%d}`, userID),
		})
	}
	do := func(method, path, auth, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", "tma "+auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	if code, _ := do("GET", "/api/exchanges/"+strconv.FormatInt(exID, 10), "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no init data: want 401, got %d", code)
	}
	if code, _ := do("GET", "/api/exchanges/"+strconv.FormatInt(exID, 10), initData(7), ""); code != http.StatusForbidden {
		t.Fatalf("not an admin: want 403, got %d", code)
	}
	if code, _ := do("POST", "/api/exchanges/"+strconv.FormatInt(exID, 10)+"/action", initData(7), `{"action":"open"}`); code != http.StatusForbidden || len(admin.actions) != 0 {
		t.Fatalf("not an admin must not trigger actions: %d %v", code, admin.actions)
	}

	code, state := do("GET", "/api/exchanges/"+strconv.FormatInt(exID, 10), initData(42), "")
	if code != http.StatusOK {
		t.Fatalf("admin state: want 200, got %d %v", code, state)
	}
	families := state["families"].([]any)
	without := state["without_children"].([]any)
	if len(families) != 1 || len(without) != 1 {
		t.Fatalf("want 1 family and 1 user without children, got %d and %d", len(families), len(without))
	}
	fam := families[0].(map[string]any)
	if fam["name"] != "Анна Петрова" || len(fam["children"].([]any)) != 1 {
		t.Fatalf("unexpected family: %v", fam)
	}

	code, body := do("POST", "/api/exchanges/"+strconv.FormatInt(exID, 10)+"/action", initData(42), `{"action":"open"}`)
	if code != http.StatusConflict || body["error"] != "Нельзя: сейчас «черновик»." {
		t.Fatalf("refusal must reach the admin: %d %v", code, body)
	}
	if code, _ := do("POST", "/api/exchanges/"+strconv.FormatInt(exID, 10)+"/action", initData(42), `{"action":"drop_tables"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown action: want 400, got %d", code)
	}

	code, _ = do("GET", "/api/exchanges/999", initData(42), "")
	if code != http.StatusNotFound {
		t.Fatalf("unknown exchange: want 404, got %d", code)
	}
	if code, _ := do("POST", "/api/exchanges/"+strconv.FormatInt(exID, 10)+"/settings", initData(42), `{"order_deadline":"13.12.2026","delivery_by":"2026-12-25"}`); code != http.StatusBadRequest {
		t.Fatalf("malformed date: want 400, got %d", code)
	}
	if code, _ := do("GET", "/api/users", initData(7), ""); code != http.StatusForbidden {
		t.Fatalf("user base is admin-only: want 403, got %d", code)
	}

	if err := st.AddManualViolation(ctx, ex.ID, anna.ID, "подарок не по бюджету"); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/users", nil)
	req.Header.Set("Authorization", "tma "+initData(42))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var users []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&users)
	res.Body.Close()
	flagged := 0
	for _, u := range users {
		if u["red_flag"] == true {
			flagged++
			if u["name"] != "Анна Петрова" {
				t.Fatalf("wrong user flagged: %v", u)
			}
		}
	}
	if len(users) != 2 || flagged != 1 {
		t.Fatalf("want 2 users with 1 red flag, got %d users, %d flagged", len(users), flagged)
	}
	code, detail := do("GET", fmt.Sprintf("/api/users/%d", anna.ID), initData(42), "")
	if code != http.StatusOK || len(detail["violations"].([]any)) != 1 || len(detail["exchanges"].([]any)) != 1 {
		t.Fatalf("user detail: %d %v", code, detail)
	}

	res, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors") {
		t.Fatalf("index: %d, CSP %q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}
}
