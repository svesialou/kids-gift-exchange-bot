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
	"github.com/svesialou/kids-gift-exchange-bot/internal/texts"
)

type fakeAdmin struct {
	actions         []string
	texts           *texts.Service
	err             error
	parentEdits     []parentEdit
	childEdits      []childEdit
	deletedChildIDs []int64
}

type parentEdit struct {
	UserID                          int64
	FirstName, LastName, Phone, PVZ string
}

type childEdit struct {
	ChildID             int64
	FirstName, LastName string
	Age                 int
	Gender, Wishes      string
}

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
	return bot.Settings{Title: "Обмен", BudgetMin: 25}
}
func (f *fakeAdmin) SetBanned(context.Context, []int64, bool, string) (int, error) { return 0, nil }
func (f *fakeAdmin) SendMessage(context.Context, []int64, string) (int, error)     { return 0, nil }
func (f *fakeAdmin) DeleteExchange(context.Context, int64, bool) error             { return nil }
func (f *fakeAdmin) AddViolation(context.Context, int64, int64, string) error      { return nil }
func (f *fakeAdmin) EditParent(_ context.Context, userID int64, firstName, lastName, phone, pvz string) error {
	if f.err != nil {
		return f.err
	}
	f.parentEdits = append(f.parentEdits, parentEdit{UserID: userID, FirstName: firstName, LastName: lastName, Phone: phone, PVZ: pvz})
	return nil
}
func (f *fakeAdmin) EditChild(_ context.Context, childID int64, firstName, lastName string, age int, gender, wishes string) error {
	if f.err != nil {
		return f.err
	}
	f.childEdits = append(f.childEdits, childEdit{ChildID: childID, FirstName: firstName, LastName: lastName, Age: age, Gender: gender, Wishes: wishes})
	return nil
}
func (f *fakeAdmin) DeleteChild(_ context.Context, childID int64) error {
	if f.err != nil {
		return f.err
	}
	f.deletedChildIDs = append(f.deletedChildIDs, childID)
	return nil
}
func (f *fakeAdmin) SetViolationDismissed(context.Context, int64, bool) error  { return nil }
func (f *fakeAdmin) UpdateSettings(context.Context, int64, bot.Settings) error { return nil }
func (f *fakeAdmin) Texts() *texts.Service                                     { return f.texts }
func (f *fakeAdmin) InviteLink(ex store.Exchange) string {
	return "https://t.me/test_bot?start=" + ex.InviteCode
}

func testStore(t *testing.T) *store.Store { return storetest.New(t) }

func TestPanelAccessAndState(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	exID, err := st.CreateExchange(ctx, store.ExchangeSchedule{Title: "Новый год", BudgetMin: 25,
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
	admin := &fakeAdmin{texts: texts.New(st)}
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

	code, body = do("POST", "/api/texts/give.thanks", initData(42), `{"value":"Спасибо, {parent}!"}`)
	if code != http.StatusConflict || !strings.Contains(body["error"].(string), "{parent}") {
		t.Fatalf("unknown placeholder must be refused with a reason: %d %v", code, body)
	}
	if code, _ := do("POST", "/api/texts/give.thanks", initData(7), `{"value":"x"}`); code != http.StatusForbidden {
		t.Fatalf("texts are admin-only: %d", code)
	}
	if code, body := do("POST", "/api/texts/give.thanks", initData(42), `{"value":"Спасибо за **{child}**!"}`); code != http.StatusOK {
		t.Fatalf("valid text must save: %d %v", code, body)
	}
	if got := admin.texts.Render("give.thanks", "child", "Ваня"); got != "Спасибо за <b>Ваня</b>!" {
		t.Fatalf("saved text not used: %q", got)
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

func TestFamilyEditing(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	exID, err := st.CreateExchange(ctx, store.ExchangeSchedule{Title: "Новый год", BudgetMin: 25,
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUser(ctx, store.User{TgID: 1001, FirstName: "Анна", LastName: "Петрова", Phone: "+375****2233", PVZAddress: "Минск, Победителей 1"}); err != nil {
		t.Fatal(err)
	}
	anna, _ := st.UserByTG(ctx, 1001)
	if err := st.AddChild(ctx, exID, store.Child{ParentID: anna.ID, FirstName: "Маша", LastName: "Петрова", Age: 6, Gender: "girl", Wishes: "кукла"}); err != nil {
		t.Fatal(err)
	}
	children, err := st.ExchangeChildren(ctx, exID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children: %d %v", len(children), err)
	}
	childID := children[0].ID

	cfg := config.Config{Location: time.UTC, TelegramToken: testToken, AdminIDs: []int64{42}, StuckAfter: 96 * time.Hour}
	admin := &fakeAdmin{texts: texts.New(st)}
	srv := httptest.NewServer(New(cfg, st, admin).Handler())
	defer srv.Close()

	initData := func() string {
		return signInitData(t, testToken, map[string]string{
			"auth_date": strconv.FormatInt(time.Now().Unix(), 10),
			"user":      `{"id":42}`,
		})
	}
	do := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "tma "+initData())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	code, body := do("POST", fmt.Sprintf("/api/users/%d/edit", anna.ID),
		`{"first_name":"Анна","last_name":"Смирнова","phone":"+375 29 111-22-33","pvz":"Минск, Немига 5"}`)
	if code != http.StatusOK || body["message"] != "Изменения сохранены." {
		t.Fatalf("parent edit: want 200, got %d %v", code, body)
	}
	want := parentEdit{UserID: anna.ID, FirstName: "Анна", LastName: "Смирнова", Phone: "+375 29 111-22-33", PVZ: "Минск, Немига 5"}
	if len(admin.parentEdits) != 1 || admin.parentEdits[0] != want {
		t.Fatalf("parent edit not passed to the bot: %v", admin.parentEdits)
	}

	code, body = do("POST", fmt.Sprintf("/api/children/%d/edit", childID),
		`{"first_name":"Маша","last_name":"Смирнова","age":7,"gender":"girl","wishes":"ролики"}`)
	if code != http.StatusOK || body["message"] != "Изменения сохранены." {
		t.Fatalf("child edit: want 200, got %d %v", code, body)
	}
	wantChild := childEdit{ChildID: childID, FirstName: "Маша", LastName: "Смирнова", Age: 7, Gender: "girl", Wishes: "ролики"}
	if len(admin.childEdits) != 1 || admin.childEdits[0] != wantChild {
		t.Fatalf("child edit not passed to the bot: %v", admin.childEdits)
	}

	code, body = do("POST", fmt.Sprintf("/api/children/%d/delete", childID), "")
	if code != http.StatusOK || body["message"] != "Участник удалён." {
		t.Fatalf("child delete: want 200, got %d %v", code, body)
	}
	if len(admin.deletedChildIDs) != 1 || admin.deletedChildIDs[0] != childID {
		t.Fatalf("child delete not passed to the bot: %v", admin.deletedChildIDs)
	}

	code, state := do("GET", fmt.Sprintf("/api/exchanges/%d", exID), "")
	families := state["families"].([]any)
	fam := families[0].(map[string]any)
	kid := fam["children"].([]any)[0].(map[string]any)
	if code != http.StatusOK || fam["first_name"] != "Анна" || fam["last_name"] != "Петрова" || kid["last_name"] != "Петрова" {
		t.Fatalf("family view must carry split names: %d %v", code, fam)
	}

	admin.err = &bot.ActionError{Msg: "Нельзя удалить: подарок уже едет."}
	code, body = do("POST", fmt.Sprintf("/api/children/%d/edit", childID), `{"first_name":"Маша","last_name":"Петрова","age":6,"gender":"girl"}`)
	if code != http.StatusConflict || body["error"] != "Нельзя удалить: подарок уже едет." {
		t.Fatalf("refusal must reach the admin as 409: %d %v", code, body)
	}
	admin.err = nil

	if code, _ := do("POST", "/api/users/0/edit", `{"first_name":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("bad user id: want 400, got %d", code)
	}
	if code, _ := do("POST", "/api/children/notanumber/edit", `{}`); code != http.StatusBadRequest {
		t.Fatalf("non-numeric child id: want 400, got %d", code)
	}
	if code, _ := do("POST", fmt.Sprintf("/api/users/%d/edit", anna.ID), `{"first_name":`); code != http.StatusBadRequest {
		t.Fatalf("malformed body: want 400, got %d", code)
	}
}

func TestEditAdultParticipantPersists(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	exID, err := st.CreateExchange(ctx, store.ExchangeSchedule{Title: "Новый год", BudgetMin: 25,
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUser(ctx, store.User{TgID: 1001, FirstName: "Анна", LastName: "Петрова", Phone: "+375****2233", PVZAddress: "Минск, Победителей 1"}); err != nil {
		t.Fatal(err)
	}
	anna, _ := st.UserByTG(ctx, 1001)
	if err := st.AddChild(ctx, exID, store.Child{ParentID: anna.ID, FirstName: "Мария", LastName: "Петрова", Age: 6, Gender: "girl"}); err != nil {
		t.Fatal(err)
	}
	children, err := st.ExchangeChildren(ctx, exID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children: %d %v", len(children), err)
	}
	childID := children[0].ID

	cfg := config.Config{Location: time.UTC, TelegramToken: testToken, AdminIDs: []int64{42}, StuckAfter: 96 * time.Hour}
	admin := bot.New(nil, st, cfg)
	srv := httptest.NewServer(New(cfg, st, admin).Handler())
	defer srv.Close()

	initData := func() string {
		return signInitData(t, testToken, map[string]string{
			"auth_date": strconv.FormatInt(time.Now().Unix(), 10),
			"user":      `{"id":42}`,
		})
	}
	req, _ := http.NewRequest("POST", srv.URL+fmt.Sprintf("/api/children/%d/edit", childID),
		strings.NewReader(`{"first_name":"Мария","last_name":"Петрова","age":30,"gender":"male","wishes":"книга"}`))
	req.Header.Set("Authorization", "tma "+initData())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != http.StatusOK || body["message"] != "Изменения сохранены." {
		t.Fatalf("adult edit: want 200, got %d %v", res.StatusCode, body)
	}

	got, err := st.ChildByID(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	want := store.ChildWithExchange{Child: store.Child{ID: childID, ParentID: anna.ID,
		FirstName: "Мария", LastName: "Петрова", Age: 30, Gender: "male", Wishes: "книга"}, ExchangeID: exID}
	if got != want {
		t.Fatalf("persisted participant mismatch:\n got %+v\nwant %+v", got, want)
	}
}
