package store_test

import (
	"context"
	"errors"
	"math/rand"
	"regexp"
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

func TestEveryExchangeGetsItsOwnInviteCode(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	sch := store.ExchangeSchedule{Title: "Новый год", BudgetMin: 25,
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)}
	ids := map[string]int64{}
	for _, name := range []string{"first", "second"} {
		id, err := st.CreateExchange(ctx, sch)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	first, err := st.ExchangeByID(ctx, ids["first"])
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.ExchangeByID(ctx, ids["second"])
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{first.InviteCode, second.InviteCode} {
		if !inviteCodeRe.MatchString(code) {
			t.Fatalf("invite code must be 12 url-safe characters, got %q", code)
		}
	}
	if first.InviteCode == second.InviteCode {
		t.Fatalf("exchanges must not share an invite code, both %q", first.InviteCode)
	}
}

var inviteCodeRe = regexp.MustCompile(`^[a-z2-9]{12}$`)

func TestExchangeScheduleAndHistory(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if cur, err := st.ActiveExchange(ctx); err != nil || cur.ID != 0 || cur.Status != domain.ExchangeNone {
		t.Fatalf("empty database must have no active exchange: %+v %v", cur, err)
	}

	opens := time.Date(2026, 11, 25, 7, 0, 0, 0, time.UTC)
	closes := time.Date(2026, 12, 5, 18, 0, 0, 0, time.UTC)
	sch := store.ExchangeSchedule{Title: "Новый год", BudgetMin: 30, RegistrationOpensAt: &opens, RegistrationClosesAt: &closes,
		OrderDeadline: time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC), DeliveryDeadline: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)}

	firstID, err := st.CreateExchange(ctx, sch)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := st.CreateExchange(ctx, sch)
	if err != nil {
		t.Fatalf("several exchanges can be planned: %v", err)
	}
	if cur, _ := st.ActiveExchange(ctx); cur.ID != 0 {
		t.Fatalf("planned exchanges are not active, got %d", cur.ID)
	}

	sch.Title, sch.BudgetMin = "Новый год 2026", 35
	if err := st.SaveExchangeSchedule(ctx, firstID, sch); err != nil {
		t.Fatal(err)
	}
	got, err := st.ExchangeByID(ctx, firstID)
	if err != nil || got.Title != "Новый год 2026" || *got.BudgetMin != 35 || !got.RegistrationOpensAt.Equal(opens) || !got.RegistrationClosesAt.Equal(closes) {
		t.Fatalf("schedule not read back: %+v %v", got, err)
	}

	ok, err := st.TransitionExchange(ctx, firstID, domain.ExchangeOpen, domain.ExchangeDraft)
	mustTrue(t, ok, err, "open first exchange")
	cur, _ := st.ActiveExchange(ctx)
	list, err := st.ListExchanges(ctx)
	if err != nil || cur.ID != firstID || len(list) != 2 || list[0].ID != secondID {
		t.Fatalf("active %d, list %+v, %v", cur.ID, list, err)
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
	if cur, _ := st.ActiveExchange(ctx); cur.ID != 0 {
		t.Fatalf("no exchange must remain, got %d", cur.ID)
	}
}

func TestUpdateUser(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u := mustUser(t, st, 1)
	ok, err := st.UpdateUser(ctx, u.ID, store.User{FirstName: "Анна", LastName: "Петрова", Phone: "+375291112233", PVZAddress: "Гомель"})
	mustTrue(t, ok, err, "update user")

	got, err := st.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := store.User{ID: u.ID, TgID: 1, FirstName: "Анна", LastName: "Петрова", Phone: "+375291112233", PVZAddress: "Гомель", BanReason: ""}
	if got.TgID != want.TgID || got.TgUsername != want.TgUsername || got.FirstName != want.FirstName ||
		got.LastName != want.LastName || got.Phone != want.Phone || got.PVZAddress != want.PVZAddress {
		t.Fatalf("UserByID after update = %+v, want %+v", got, want)
	}

	ok, err = st.UpdateUser(ctx, u.ID+1000, store.User{FirstName: "x"})
	mustFalse(t, ok, err, "update nonexistent user")
}

func TestUpdateChild(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ex := openExchange(t, st)
	a := mustUser(t, st, 1)
	for _, name := range []struct{ first, last, wishes string }{
		{"Коля", "Иванов", "мячик"},
		{"Маша", "Иванова", "кукла"},
	} {
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: a.ID, FirstName: name.first, LastName: name.last, Age: 6, Gender: "boy", Wishes: name.wishes}); err != nil {
			t.Fatal(err)
		}
	}
	kids, err := st.ChildrenOf(ctx, ex.ID, a.ID)
	if err != nil || len(kids) != 2 {
		t.Fatalf("children of user: %d %v", len(kids), err)
	}
	updatedID, other := kids[0].ID, kids[1]

	ok, err := st.UpdateChild(ctx, updatedID, store.Child{FirstName: "Никита", LastName: "Петров", Age: 9, Gender: "boy", Wishes: "конструктор"})
	mustTrue(t, ok, err, "update child")

	kids, err = st.ChildrenOf(ctx, ex.ID, a.ID)
	if err != nil || len(kids) != 2 {
		t.Fatalf("children of user after update: %d %v", len(kids), err)
	}
	want := store.Child{ID: updatedID, ParentID: a.ID, FirstName: "Никита", LastName: "Петров", Age: 9, Gender: "boy", Wishes: "конструктор"}
	if kids[0] != want {
		t.Fatalf("updated child = %+v, want %+v", kids[0], want)
	}
	wantOther := store.Child{ID: other.ID, ParentID: a.ID, FirstName: "Маша", LastName: "Иванова", Age: 6, Gender: "boy", Wishes: "кукла"}
	if kids[1] != wantOther {
		t.Fatalf("untouched child = %+v, want %+v", kids[1], wantOther)
	}

	ok, err = st.SoftDeleteChild(ctx, other.ID)
	mustTrue(t, ok, err, "soft delete other child")
	ok, err = st.UpdateChild(ctx, other.ID, store.Child{FirstName: "x"})
	mustFalse(t, ok, err, "update soft-deleted child")
	ok, err = st.UpdateChild(ctx, other.ID+1000, store.Child{FirstName: "x"})
	mustFalse(t, ok, err, "update nonexistent child")
}

func TestSoftDeleteChild(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ex := openExchange(t, st)
	a := mustUser(t, st, 1)
	for _, u := range []store.User{a, a, mustUser(t, st, 2)} {
		if err := st.AddChild(ctx, ex.ID, store.Child{ParentID: u.ID, FirstName: "Kid", LastName: "L", Age: 5, Gender: "boy"}); err != nil {
			t.Fatal(err)
		}
	}
	kids, err := st.ChildrenOf(ctx, ex.ID, a.ID)
	if err != nil || len(kids) != 2 {
		t.Fatalf("children of user: %d %v", len(kids), err)
	}
	victim, sibling := kids[0], kids[1]

	ok, err := st.ChildHasAssignment(ctx, victim.ID)
	if err != nil || ok {
		t.Fatalf("ChildHasAssignment before distribute = %v, %v", ok, err)
	}

	ok, err = st.SoftDeleteChild(ctx, victim.ID)
	mustTrue(t, ok, err, "soft delete child")

	rest, err := st.ChildrenOf(ctx, ex.ID, a.ID)
	if err != nil || len(rest) != 1 || rest[0].ID != sibling.ID {
		t.Fatalf("children of user after soft delete: %+v %v", rest, err)
	}
	if _, err := st.ChildByID(ctx, rest[0].ID); err != nil {
		t.Fatalf("ChildByID remaining child: %v", err)
	}
	_, err = st.ChildByID(ctx, victim.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ChildByID soft-deleted child = %v, want ErrNotFound", err)
	}

	participants, err := st.Participants(ctx, ex.ID)
	if err != nil || len(participants) != 2 {
		t.Fatalf("participants after soft delete: %d %v", len(participants), err)
	}
	for _, p := range participants {
		if p.ID == a.ID && p.Children != 1 {
			t.Fatalf("parent children count = %d, want 1", p.Children)
		}
	}

	ok, err = st.SoftDeleteChild(ctx, victim.ID)
	mustFalse(t, ok, err, "soft delete twice")

	_, _ = st.TransitionExchange(ctx, ex.ID, domain.ExchangeClosed, domain.ExchangeOpen)
	if _, err := st.Distribute(ctx, ex.ID, func(r []domain.ChildRef) ([]domain.Pair, error) {
		return domain.Distribute(r, rand.New(rand.NewSource(1)))
	}); err != nil {
		t.Fatal(err)
	}
	ok, err = st.ChildHasAssignment(ctx, rest[0].ID)
	mustTrue(t, ok, err, "remaining child has assignment")

	ok, err = st.SoftDeleteChild(ctx, rest[0].ID)
	mustFalse(t, ok, err, "soft delete child with assignment")
}

func TestSentMessageHistory(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	a, b, silent := mustUser(t, st, 1), mustUser(t, st, 2), mustUser(t, st, 3)
	for _, m := range []struct {
		userID int64
		kind   string
		text   string
	}{
		{a.ID, "org", "первое сообщение"},
		{b.ID, "org", "чужое сообщение"},
		{a.ID, "org", "второе сообщение"},
	} {
		if err := st.RecordSentMessage(ctx, m.userID, m.kind, m.text); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.SentMessagesOfUser(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.SentMessage{
		{ID: got[0].ID, UserID: a.ID, Kind: "org", Text: "второе сообщение", CreatedAt: got[0].CreatedAt},
		{ID: got[1].ID, UserID: a.ID, Kind: "org", Text: "первое сообщение", CreatedAt: got[1].CreatedAt},
	}
	if len(got) != len(want) || got[0].Text != want[0].Text || got[1].Text != want[1].Text ||
		got[0].ID <= got[1].ID {
		t.Fatalf("SentMessagesOfUser = %+v, want newest first %+v", got, want)
	}

	got, err = st.SentMessagesOfUser(ctx, b.ID)
	if err != nil || len(got) != 1 || got[0].Text != "чужое сообщение" {
		t.Fatalf("messages of second user = %+v %v", got, err)
	}
	if got, _ := st.SentMessagesOfUser(ctx, silent.ID); got != nil {
		t.Fatalf("user without messages = %+v, want nil", got)
	}
}

func openExchange(t *testing.T, st *store.Store) store.Exchange {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateExchange(ctx, store.ExchangeSchedule{Title: "t", BudgetMin: 25,
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
