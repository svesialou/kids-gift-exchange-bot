package store_test

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store/storetest"
)

func newTestStore(t *testing.T) *store.Store { return storetest.New(t) }

func mustUser(t *testing.T, st *store.Store, tgID int64) store.User {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveUser(ctx, store.User{TgID: tgID, FirstName: "F", LastName: "L", Phone: "+375291234567", PVZAddress: "Minsk"}); err != nil {
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

	ex := openExchange(t, st)

	a, b, c := mustUser(t, st, 1), mustUser(t, st, 2), mustUser(t, st, 3)
	for _, u := range []store.User{a, a, b, c} {
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "boy"}); err != nil {
			t.Fatal(err)
		}
	}
	kidsOfC, _ := st.ChildrenOf(ctx, ex.ID, c.ID)

	ok, err := st.DeleteChild(ctx, ex.ID, a.ID, kidsOfC[0].ID)
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

func TestExchangeScheduleAndHistory(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if cur, err := st.CurrentExchange(ctx); err != nil || cur.ID != 0 || cur.Status != domain.ExchangeNone {
		t.Fatalf("empty database must have no exchange: %+v %v", cur, err)
	}

	opens := time.Date(2026, 11, 25, 7, 0, 0, 0, time.UTC)
	closes := time.Date(2026, 12, 5, 18, 0, 0, 0, time.UTC)
	sch := store.ExchangeSchedule{Title: "Новый год", BudgetMax: 30, Currency: "BYN", RegistrationOpensAt: &opens, RegistrationClosesAt: &closes,
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)}

	firstID, err := st.CreateExchange(ctx, sch)
	if err != nil {
		t.Fatalf("the first exchange must be creatable: %v", err)
	}
	if _, err := st.CreateExchange(ctx, sch); err == nil {
		t.Fatal("a new exchange must not start while the current one is not completed")
	}
	sch.Title, sch.BudgetMax = "Новый год 2026", 35
	if err := st.SaveExchangeSchedule(ctx, firstID, sch); err != nil {
		t.Fatal(err)
	}
	got, err := st.ExchangeByID(ctx, firstID)
	if err != nil || got.Title != "Новый год 2026" || *got.BudgetMax != 35 || !got.RegistrationOpensAt.Equal(opens) || !got.RegistrationClosesAt.Equal(closes) {
		t.Fatalf("schedule not read back: %+v %v", got, err)
	}

	ok, err := st.TransitionExchange(ctx, firstID, domain.ExchangeCompleted, domain.ExchangeDraft)
	mustTrue(t, ok, err, "complete first exchange")
	secondID, err := st.CreateExchange(ctx, sch)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := st.CurrentExchange(ctx)
	list, err := st.ListExchanges(ctx)
	if err != nil || cur.ID != secondID || len(list) != 2 || list[0].ID != secondID {
		t.Fatalf("history: current %d, list %+v, %v", cur.ID, list, err)
	}
}

func TestViolationsAndBans(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	ex := openExchange(t, st)

	a, b, c, banned := mustUser(t, st, 1), mustUser(t, st, 2), mustUser(t, st, 3), mustUser(t, st, 4)
	for _, u := range []store.User{a, b, c, banned} {
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "boy"}); err != nil {
			t.Fatal(err)
		}
	}
	ok, err := st.SetBanned(ctx, banned.ID, true, "не отправил подарок в прошлом году", now)
	mustTrue(t, ok, err, "ban")
	refs, _ := st.ChildRefs(ctx, ex.ID)
	if len(refs) != 3 {
		t.Fatalf("children of banned parents must be left out of distribution, got %d refs", len(refs))
	}

	_, _ = st.TransitionExchange(ctx, ex.ID, domain.ExchangeClosed, domain.ExchangeOpen)
	if _, err := st.Distribute(ctx, ex.ID, func(r []domain.ChildRef) ([]domain.Pair, error) {
		return domain.Distribute(r, rand.New(rand.NewSource(1)))
	}); err != nil {
		t.Fatal(err)
	}
	list, _ := st.AssignmentsByExchange(ctx, ex.ID)
	ordered := list[0]
	ok, err = st.MarkOrdered(ctx, ordered.ID, ordered.Giver.ID, nil, now)
	mustTrue(t, ok, err, "order one gift")

	n, err := st.RecordOrderViolations(ctx, ex.ID, now)
	if err != nil || n != 2 {
		t.Fatalf("want 2 order violations, got %d %v", n, err)
	}
	if n, err := st.RecordOrderViolations(ctx, ex.ID, now); err != nil || n != 0 {
		t.Fatalf("order check must run once, second run recorded %d %v", n, err)
	}
	n, err = st.RecordDeliveryViolations(ctx, ex.ID, now)
	if err != nil || n != 3 {
		t.Fatalf("want 3 delivery violations (nothing arrived), got %d %v", n, err)
	}

	if err := st.AddManualViolation(ctx, ex.ID, a.ID, "подарок не по бюджету"); err != nil {
		t.Fatal(err)
	}
	vs, err := st.ViolationsByExchange(ctx, ex.ID)
	if err != nil || len(vs) != 6 {
		t.Fatalf("want 6 violations, got %d %v", len(vs), err)
	}
	ok, err = st.SetViolationDismissed(ctx, vs[0].ID, true, now)
	mustTrue(t, ok, err, "dismiss")

	stats, err := st.UserStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range stats {
		total += s.Violations
		if s.ID == banned.ID && (s.BannedAt == nil || s.BanReason == "") {
			t.Fatalf("ban not visible in stats: %+v", s)
		}
	}
	if total != 5 {
		t.Fatalf("dismissed violations must not count: want 5, got %d", total)
	}
	ok, err = st.SetBanned(ctx, banned.ID, false, "", now)
	mustTrue(t, ok, err, "unban")
	u, _ := st.UserByID(ctx, banned.ID)
	if u.BannedAt != nil || u.BanReason != "" {
		t.Fatalf("unban did not clear: %+v", u)
	}
}

func TestDeleteDistributedExchange(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	ex := openExchange(t, st)
	for i := 1; i <= 3; i++ {
		u := mustUser(t, st, int64(i))
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "boy"}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = st.TransitionExchange(ctx, ex.ID, domain.ExchangeClosed, domain.ExchangeOpen)
	if _, err := st.Distribute(ctx, ex.ID, func(r []domain.ChildRef) ([]domain.Pair, error) {
		return domain.Distribute(r, rand.New(rand.NewSource(1)))
	}); err != nil {
		t.Fatal(err)
	}
	list, _ := st.AssignmentsByExchange(ctx, ex.ID)
	if _, err := st.MarkReminderSent(ctx, list[0].ID, domain.RemindGiverOrder, "2026-12-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordOrderViolations(ctx, ex.ID, now); err != nil {
		t.Fatal(err)
	}

	ok, err := st.DeleteExchange(ctx, ex.ID)
	mustTrue(t, ok, err, "delete distributed exchange")
	if a, _ := st.AssignmentsByExchange(ctx, ex.ID); len(a) != 0 {
		t.Fatalf("assignments left: %d", len(a))
	}
	if v, _ := st.ViolationsByExchange(ctx, ex.ID); len(v) != 0 {
		t.Fatalf("violations left: %d", len(v))
	}
	if cur, _ := st.CurrentExchange(ctx); cur.ID != 0 {
		t.Fatalf("no exchange must remain, got %d", cur.ID)
	}
}

func openExchange(t *testing.T, st *store.Store) store.Exchange {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateExchange(ctx, store.ExchangeSchedule{Title: "t", BudgetMax: 25, Currency: "BYN",
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := st.TransitionExchange(ctx, id, domain.ExchangeOpen, domain.ExchangeDraft)
	mustTrue(t, ok, err, "open")
	ex, _ := st.ExchangeByID(ctx, id)
	return ex
}

func TestSessionRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.SaveSession(ctx, 42, store.Session{State: "reg_last", Data: map[string]string{"first_name": "Аня"}}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Session(ctx, 42)
	if err != nil || sess.State != "reg_last" || sess.Data["first_name"] != "Аня" {
		t.Fatalf("got %+v %v", sess, err)
	}
	if err := st.SaveSession(ctx, 42, store.Session{}); err != nil {
		t.Fatal(err)
	}
	sess, err = st.Session(ctx, 42)
	if err != nil || sess.State != "" {
		t.Fatalf("session not cleared: %+v %v", sess, err)
	}
}
