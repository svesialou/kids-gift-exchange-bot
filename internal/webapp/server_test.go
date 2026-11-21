package webapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/svesialou/kids-gift-exchange-bot/internal/bot"
	"github.com/svesialou/kids-gift-exchange-bot/internal/config"
	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

type fakeAdmin struct{ actions []string }

func (f *fakeAdmin) ChangeExchangeStatus(_ context.Context, action string) (domain.ExchangeStatus, error) {
	f.actions = append(f.actions, action)
	return "", &bot.ActionError{Msg: "Нельзя: сейчас «черновик»."}
}
func (f *fakeAdmin) NewExchange(context.Context) error { return nil }
func (f *fakeAdmin) PreviewDistribution(context.Context) (bot.DistributionPreview, error) {
	return bot.DistributionPreview{Families: 1, Children: 2}, nil
}
func (f *fakeAdmin) Distribute(context.Context) (int, error)            { return 0, nil }
func (f *fakeAdmin) RemindNow(context.Context) (int, error)             { return 0, nil }
func (f *fakeAdmin) Reassign(context.Context, int64, int64) error       { return nil }
func (f *fakeAdmin) Broadcast(context.Context, string) (int, error)     { return 0, nil }
func (f *fakeAdmin) Settings() bot.Settings                             { return bot.Settings{BudgetMax: 25, Currency: "BYN"} }
func (f *fakeAdmin) UpdateSettings(context.Context, bot.Settings) error { return nil }
func (f *fakeAdmin) InviteLink() string                                 { return "https://t.me/test_bot?start=x" }

func testStore(t *testing.T) *store.Store {
	t.Helper()
	root := os.Getenv("TEST_MYSQL_DSN")
	if root == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("mysql", root)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("kge_web_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(ctx, "DROP DATABASE "+name)
		_ = admin.Close()
	})
	st, err := store.Open(ctx, root+name+"?parseTime=true&loc=UTC&multiStatements=true&charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPanelAccessAndState(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	ex, err := st.CurrentExchange(ctx)
	if err != nil {
		t.Fatal(err)
	}
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

	cfg := config.Config{TelegramToken: testToken, AdminIDs: []int64{42}, GiftBudgetMax: 25, GiftCurrency: "BYN", StuckAfter: 96 * time.Hour}
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

	if code, _ := do("GET", "/api/state", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no init data: want 401, got %d", code)
	}
	if code, _ := do("GET", "/api/state", initData(7), ""); code != http.StatusForbidden {
		t.Fatalf("not an admin: want 403, got %d", code)
	}
	if code, _ := do("POST", "/api/action", initData(7), `{"action":"open"}`); code != http.StatusForbidden || len(admin.actions) != 0 {
		t.Fatalf("not an admin must not trigger actions: %d %v", code, admin.actions)
	}

	code, state := do("GET", "/api/state", initData(42), "")
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

	code, body := do("POST", "/api/action", initData(42), `{"action":"open"}`)
	if code != http.StatusConflict || body["error"] != "Нельзя: сейчас «черновик»." {
		t.Fatalf("refusal must reach the admin: %d %v", code, body)
	}
	if code, _ := do("POST", "/api/action", initData(42), `{"action":"drop_tables"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown action: want 400, got %d", code)
	}

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors") {
		t.Fatalf("index: %d, CSP %q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}
}
