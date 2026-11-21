package bot

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

const (
	stateOrderDate = "order_date"
	stateCode      = "code"
)

func (b *Bot) distributedExchange(ctx context.Context, tgID int64) (store.Exchange, bool, error) {
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return store.Exchange{}, false, err
	}
	if ex.ID == 0 {
		b.send(tgID, b.tx("common.no_exchange"), nil)
		return ex, false, nil
	}
	if ex.Status != domain.ExchangeDistributed {
		b.send(tgID, b.tx("give.not_distributed"), nil)
		return ex, false, nil
	}
	return ex, true, nil
}

// ---------- giver side ----------

func (b *Bot) showGiverCards(ctx context.Context, user store.User) error {
	ex, ok, err := b.distributedExchange(ctx, user.TgID)
	if err != nil || !ok {
		return err
	}
	list, err := b.store.AssignmentsByGiver(ctx, ex.ID, user.ID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		b.send(user.TgID, b.tx("give.none"), nil)
		return nil
	}
	for _, a := range list {
		b.sendGiverCard(a, "")
	}
	return nil
}

func (b *Bot) giverCardText(a store.Assignment) string {
	wishes := a.ChildWishes
	if wishes == "" {
		wishes = b.label("give.no_wishes")
	}
	return b.tx("give.card",
		"child_full", a.ChildFirstName+" "+a.ChildLastName, "age", ageText(a.ChildAge), "gender", genderText(a.ChildGender),
		"wishes", wishes, "parent", a.Parent.FirstName+" "+a.Parent.LastName, "pvz", a.Parent.PVZAddress,
		"budget", b.budgetText(), "order_deadline", b.orderDeadlineText(), "status", b.statusText(a))
}

func (b *Bot) giverButtons(a store.Assignment) any {
	switch a.Status {
	case domain.AssignmentAssigned:
		return inlineButtons(button(b.label("btn.ordered"), "ord", a.ID))
	case domain.AssignmentOrdered:
		return inlineButtons(button(b.label("btn.arrived"), "arr", a.ID))
	case domain.AssignmentArrived:
		return inlineButtons(button(b.label("btn.send_code"), "code", a.ID))
	}
	return nil
}

func (b *Bot) sendGiverCard(a store.Assignment, header string) {
	text := b.giverCardText(a)
	if header != "" {
		text = header + "\n\n" + text
	}
	b.send(a.Giver.TgID, text, b.giverButtons(a))
}

// statusText is a plain value inserted into other texts.
func (b *Bot) statusText(a store.Assignment) string {
	switch a.Status {
	case domain.AssignmentAssigned:
		return b.label("status.assigned")
	case domain.AssignmentOrdered:
		if a.ExpectedDate != nil {
			return b.label("status.ordered_expected", "date", a.ExpectedDate.Format("02.01"))
		}
		return b.label("status.ordered")
	case domain.AssignmentArrived:
		if a.PickupState().CodeValid(b.now()) {
			return b.label("status.arrived_code_sent")
		}
		if a.PickupState().CodeRequestPending() {
			return b.label("status.arrived_code_wait")
		}
		return b.label("status.arrived")
	case domain.AssignmentReceived:
		return b.label("status.received")
	}
	return string(a.Status)
}

// giverAssignment loads an assignment and checks that the user is its giver.
func (b *Bot) giverAssignment(ctx context.Context, user store.User, id int64) (store.Assignment, bool, error) {
	a, err := b.store.Assignment(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.Giver.ID != user.ID) {
		b.send(user.TgID, b.tx("give.not_yours"), nil)
		return store.Assignment{}, false, nil
	}
	if err != nil {
		return store.Assignment{}, false, err
	}
	return a, true, nil
}

func (b *Bot) startOrdered(ctx context.Context, user store.User, id int64) error {
	a, ok, err := b.giverAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	if a.Status != domain.AssignmentAssigned {
		b.send(user.TgID, b.tx("give.already_ordered"), nil)
		return nil
	}
	sess := store.Session{State: stateOrderDate, Data: map[string]string{"aid": strconv.FormatInt(id, 10)}}
	if err := b.store.SaveSession(ctx, user.TgID, sess); err != nil {
		return err
	}
	b.send(user.TgID, b.tx("give.ask_date", "child", a.ChildFirstName), b.stepKeyboard(b.label("btn.dont_know")))
	return nil
}

func (b *Bot) handleOrderDate(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	var expected *time.Time
	if text != b.label("btn.dont_know") {
		d, ok := parseDayMonth(text, b.now().In(b.cfg.Location))
		if !ok {
			b.send(tgID, b.tx("give.bad_date"), nil)
			return nil
		}
		expected = &d
	}

	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}
	user, err := b.store.UserByTG(ctx, tgID)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(sess.Data["aid"], 10, 64)
	ok, err := b.store.MarkOrdered(ctx, id, user.ID, expected, b.now())
	if err != nil {
		return err
	}
	if !ok {
		return b.showMenu(ctx, tgID, b.tx("give.mark_failed"))
	}
	a, err := b.store.Assignment(ctx, id)
	if err != nil {
		return err
	}

	when := b.label("recv.when_unknown")
	if expected != nil {
		when = b.label("recv.when_date", "date", expected.Format("02.01"))
	}
	b.send(a.Parent.TgID, b.tx("recv.ordered", "child", a.ChildFirstName, "when", when), nil)
	b.send(tgID, b.tx("give.ordered_ok"), b.mainMenu())
	b.sendGiverCard(a, "")
	return nil
}

func (b *Bot) markArrived(ctx context.Context, user store.User, id int64) error {
	ok, err := b.store.MarkArrived(ctx, id, user.ID, b.now())
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, b.tx("give.mark_failed"), nil)
		return nil
	}
	a, err := b.store.Assignment(ctx, id)
	if err != nil {
		return err
	}
	b.send(a.Parent.TgID, b.tx("recv.arrived", "child", a.ChildFirstName, "pvz", a.Parent.PVZAddress),
		inlineButtons(button(b.label("btn.going"), "pick", a.ID)))
	b.send(user.TgID, b.tx("give.arrived_ok"),
		inlineButtons(button(b.label("btn.send_code"), "code", a.ID)))
	return nil
}

func (b *Bot) startCode(ctx context.Context, user store.User, id int64) error {
	a, ok, err := b.giverAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	if a.Status == domain.AssignmentReceived {
		b.send(user.TgID, b.tx("give.already_received"), nil)
		return nil
	}
	sess := store.Session{State: stateCode, Data: map[string]string{"aid": strconv.FormatInt(id, 10)}}
	if err := b.store.SaveSession(ctx, user.TgID, sess); err != nil {
		return err
	}
	b.send(user.TgID, b.tx("give.ask_code", "child", a.ChildFirstName), b.cancelKeyboard())
	return nil
}

func (b *Bot) handleCodeInput(ctx context.Context, msg *tgbotapi.Message, sess store.Session) error {
	tgID := msg.From.ID
	text := strings.TrimSpace(msg.Text)
	fileID := ""
	if len(msg.Photo) > 0 {
		fileID = msg.Photo[len(msg.Photo)-1].FileID
		text = strings.TrimSpace(msg.Caption)
	}
	if fileID == "" && (text == "" || utf8.RuneCountInString(text) > 255) {
		b.send(tgID, b.tx("give.bad_code_input"), nil)
		return nil
	}

	if err := b.store.SaveSession(ctx, tgID, store.Session{}); err != nil {
		return err
	}
	user, err := b.store.UserByTG(ctx, tgID)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(sess.Data["aid"], 10, 64)
	now := b.now()
	validUntil := b.policy.EndOfDay(now)
	ok, err := b.store.SetCode(ctx, id, user.ID, text, fileID, now, validUntil)
	if err != nil {
		return err
	}
	if !ok {
		return b.showMenu(ctx, tgID, b.tx("give.code_failed"))
	}
	a, err := b.store.Assignment(ctx, id)
	if err != nil {
		return err
	}
	b.sendCodeToRecipient(a)
	b.send(tgID, b.tx("give.code_sent_ok"), b.mainMenu())
	return nil
}

func (b *Bot) sendCodeToRecipient(a store.Assignment) {
	markup := inlineButtons(button(b.label("btn.received"), "got", a.ID), button(b.label("btn.bad_code"), "bad", a.ID))
	if a.CodeFileID != "" {
		caption := b.tx("recv.code_photo", "child", a.ChildFirstName, "pvz", a.Parent.PVZAddress)
		if a.CodeText != "" {
			caption = b.tx("recv.code", "child", a.ChildFirstName, "code", a.CodeText, "pvz", a.Parent.PVZAddress)
		}
		b.sendPhoto(a.Parent.TgID, a.CodeFileID, caption, markup)
		return
	}
	b.send(a.Parent.TgID, b.tx("recv.code", "child", a.ChildFirstName, "code", a.CodeText, "pvz", a.Parent.PVZAddress), markup)
}

// ---------- recipient side ----------

func (b *Bot) showRecipientCards(ctx context.Context, user store.User) error {
	ex, ok, err := b.distributedExchange(ctx, user.TgID)
	if err != nil || !ok {
		return err
	}
	list, err := b.store.AssignmentsByParent(ctx, ex.ID, user.ID)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		b.send(user.TgID, b.tx("recv.none"), nil)
		return nil
	}
	now := b.now()
	for _, a := range list {
		text := b.tx("recv.item", "child", a.ChildFirstName, "status", b.statusText(a))
		var markup any
		if a.Status == domain.AssignmentArrived {
			if a.PickupState().CodeValid(now) {
				markup = inlineButtons(button(b.label("btn.show_code"), "pick", a.ID), button(b.label("btn.received"), "got", a.ID), button(b.label("btn.bad_code"), "bad", a.ID))
			} else {
				markup = inlineButtons(button(b.label("btn.going"), "pick", a.ID))
			}
		}
		b.send(user.TgID, text, markup)
	}
	return nil
}

func (b *Bot) recipientAssignment(ctx context.Context, user store.User, id int64) (store.Assignment, bool, error) {
	a, err := b.store.Assignment(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.Parent.ID != user.ID) {
		b.send(user.TgID, b.tx("recv.not_yours"), nil)
		return store.Assignment{}, false, nil
	}
	if err != nil {
		return store.Assignment{}, false, err
	}
	return a, true, nil
}

// requestCode asks the giver for a fresh code. invalidate=true means the recipient reported the current code did not work.
func (b *Bot) requestCode(ctx context.Context, user store.User, id int64, invalidate bool) error {
	a, ok, err := b.recipientAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	switch a.Status {
	case domain.AssignmentReceived:
		b.send(user.TgID, b.tx("recv.already_received"), nil)
		return nil
	case domain.AssignmentAssigned, domain.AssignmentOrdered:
		b.send(user.TgID, b.tx("recv.not_arrived"), nil)
		return nil
	}

	now := b.now()
	if !invalidate && a.PickupState().CodeValid(now) {
		b.sendCodeToRecipient(a)
		return nil
	}
	ok, err = b.store.RequestCode(ctx, id, user.ID, invalidate, now)
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, b.tx("recv.request_failed"), nil)
		return nil
	}

	key := "give.code_requested"
	if invalidate {
		key = "give.code_rejected"
	}
	b.send(a.Giver.TgID, b.tx(key, "child", a.ChildFirstName), inlineButtons(button(b.label("btn.send_code"), "code", a.ID)))
	b.send(user.TgID, b.tx("recv.requested"), nil)
	return nil
}

func (b *Bot) markReceived(ctx context.Context, user store.User, id int64) error {
	a, ok, err := b.recipientAssignment(ctx, user, id)
	if err != nil || !ok {
		return err
	}
	ok, err = b.store.MarkReceived(ctx, id, user.ID, b.now())
	if err != nil {
		return err
	}
	if !ok {
		b.send(user.TgID, b.tx("recv.mark_failed"), nil)
		return nil
	}
	b.send(user.TgID, b.tx("recv.thanks", "child", a.ChildFirstName), nil)
	b.send(a.Giver.TgID, b.tx("give.thanks", "child", a.ChildFirstName), nil)
	return b.notifyIfAllReceived(ctx)
}

func (b *Bot) notifyIfAllReceived(ctx context.Context) error {
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Status != domain.AssignmentReceived {
			return nil
		}
	}
	b.notifyAdmins("🎉 Все подарки получены! Можно завершить обмен командой /finish.")
	return nil
}

// parseDayMonth parses "DD.MM" and picks the year so the date is not far in the past (December orders may arrive in January).
func parseDayMonth(s string, now time.Time) (time.Time, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "/", ".")
	d, err := time.Parse("2.1", s)
	if err != nil {
		d, err = time.Parse("02.01", s)
		if err != nil {
			return time.Time{}, false
		}
	}
	date := time.Date(now.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	if date.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)) {
		date = date.AddDate(1, 0, 0)
	}
	return date, true
}
