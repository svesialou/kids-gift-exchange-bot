package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
)

// newTestStore needs TEST_MYSQL_DSN pointing at a server root, e.g. root:secret@tcp(127.0.0.1:3307)/
func newTestStore(t *testing.T) *Store {
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
	name := fmt.Sprintf("kge_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(ctx, "DROP DATABASE "+name)
		_ = admin.Close()
	})

	st, err := Open(ctx, root+name+"?parseTime=true&loc=UTC&multiStatements=true&charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func mustUser(t *testing.T, st *Store, tgID int64) User {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveUser(ctx, User{TgID: tgID, FirstName: "F", LastName: "L", Phone: "+375291234567", PVZAddress: "Minsk"}); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByTG(ctx, tgID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustTrue(t *testing.T, ok bool, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if !ok {
		t.Fatalf("%s: expected success", what)
	}
}

func mustFalse(t *testing.T, ok bool, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if ok {
		t.Fatalf("%s: expected rejection", what)
	}
}

func TestExchangeLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	ex, err := st.CurrentExchange(ctx)
	if err != nil || ex.Status != domain.ExchangeDraft {
		t.Fatalf("want draft exchange, got %+v %v", ex, err)
	}
	ok, err := st.TransitionExchange(ctx, ex.ID, domain.ExchangeOpen, domain.ExchangeDraft, domain.ExchangeClosed)
	mustTrue(t, ok, err, "open registration")

	a, b, c := mustUser(t, st, 1), mustUser(t, st, 2), mustUser(t, st, 3)
	for _, u := range []User{a, a, b, c} {
		if err := st.AddChild(ctx, ex.ID, Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "boy"}); err != nil {
			t.Fatal(err)
		}
	}
	kidsOfC, _ := st.ChildrenOf(ctx, ex.ID, c.ID)

	ok, err = st.DeleteChild(ctx, ex.ID, a.ID, kidsOfC[0].ID)
	mustFalse(t, ok, err, "delete someone else's child")

	ok, err = st.TransitionExchange(ctx, ex.ID, domain.ExchangeClosed, domain.ExchangeOpen)
	mustTrue(t, ok, err, "close registration")

	ok, err = st.DeleteChild(ctx, ex.ID, c.ID, kidsOfC[0].ID)
	mustFalse(t, ok, err, "delete child after close")

	plan := func(refs []domain.ChildRef) ([]domain.Pair, error) {
		return domain.Distribute(refs, rand.New(rand.NewSource(7)))
	}
	n, err := st.Distribute(ctx, ex.ID, plan)
	if err != nil || n != 4 {
		t.Fatalf("distribute: n=%d err=%v", n, err)
	}
	if _, err := st.Distribute(ctx, ex.ID, plan); err == nil {
		t.Fatal("second distribute must fail")
	}

	list, err := st.AssignmentsByExchange(ctx, ex.ID)
	if err != nil || len(list) != 4 {
		t.Fatalf("assignments: %d %v", len(list), err)
	}
	for _, as := range list {
		if as.Giver.ID == as.Parent.ID {
			t.Fatalf("assignment %d: giver is the parent", as.ID)
		}
	}

	as := list[0]
	stranger := a.ID
	if stranger == as.Giver.ID || stranger == as.Parent.ID {
		stranger = b.ID
	}
	if stranger == as.Giver.ID || stranger == as.Parent.ID {
		stranger = c.ID
	}

	ok, err = st.MarkOrdered(ctx, as.ID, stranger, nil, now)
	mustFalse(t, ok, err, "order by stranger")
	exp := time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)
	ok, err = st.MarkOrdered(ctx, as.ID, as.Giver.ID, &exp, now)
	mustTrue(t, ok, err, "order by giver")
	ok, err = st.MarkOrdered(ctx, as.ID, as.Giver.ID, &exp, now)
	mustFalse(t, ok, err, "order twice")

	ok, err = st.RequestCode(ctx, as.ID, as.Parent.ID, false, now)
	mustFalse(t, ok, err, "request code before arrival")

	ok, err = st.SetCode(ctx, as.ID, as.Giver.ID, "1234", "", now, now.Add(time.Hour))
	mustTrue(t, ok, err, "set code implies arrival")
	got, _ := st.Assignment(ctx, as.ID)
	if got.Status != domain.AssignmentArrived || got.ArrivedAt == nil || !got.PickupState().CodeValid(now) {
		t.Fatalf("after code: %+v", got.PickupState())
	}

	ok, err = st.RequestCode(ctx, as.ID, as.Parent.ID, true, now.Add(time.Minute))
	mustTrue(t, ok, err, "code did not work")
	got, _ = st.Assignment(ctx, as.ID)
	if got.PickupState().CodeValid(now) || !got.PickupState().CodeRequestPending() {
		t.Fatalf("after invalidate: %+v", got.PickupState())
	}

	ok, err = st.MarkReceived(ctx, as.ID, as.Giver.ID, now)
	mustFalse(t, ok, err, "received marked by giver")
	ok, err = st.MarkReceived(ctx, as.ID, as.Parent.ID, now)
	mustTrue(t, ok, err, "received by parent")

	ok, err = st.Reassign(ctx, ex.ID, as.ChildID, stranger)
	mustFalse(t, ok, err, "reassign received gift")

	other := list[1]
	ok, err = st.Reassign(ctx, ex.ID, other.ChildID, other.Parent.ID)
	mustFalse(t, ok, err, "reassign to own parent")
	newGiver := a.ID
	if newGiver == other.Parent.ID {
		newGiver = b.ID
	}
	ok, err = st.Reassign(ctx, ex.ID, other.ChildID, newGiver)
	mustTrue(t, ok, err, "reassign to another family")

	fresh, err := st.MarkReminderSent(ctx, other.ID, domain.RemindGiverOrder, "2026-12-10")
	mustTrue(t, fresh, err, "first reminder")
	fresh, err = st.MarkReminderSent(ctx, other.ID, domain.RemindGiverOrder, "2026-12-10")
	mustFalse(t, fresh, err, "duplicate reminder same day")
}

func TestSessionRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.SaveSession(ctx, 42, Session{State: "reg_last", Data: map[string]string{"first_name": "Аня"}}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Session(ctx, 42)
	if err != nil || sess.State != "reg_last" || sess.Data["first_name"] != "Аня" {
		t.Fatalf("got %+v %v", sess, err)
	}
	if err := st.SaveSession(ctx, 42, Session{}); err != nil {
		t.Fatal(err)
	}
	sess, err = st.Session(ctx, 42)
	if err != nil || sess.State != "" {
		t.Fatalf("session not cleared: %+v %v", sess, err)
	}
}
