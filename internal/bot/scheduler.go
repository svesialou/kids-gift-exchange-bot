package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

func (b *Bot) RunScheduler(ctx context.Context) {
	ticker := time.NewTicker(b.cfg.SchedulerEvery)
	defer ticker.Stop()
	for {
		if err := b.tick(ctx); err != nil {
			slog.Error("scheduler tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) tick(ctx context.Context) error {
	if err := b.advanceSchedule(ctx, b.now()); err != nil {
		return err
	}
	ex, err := b.store.ActiveExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeDistributed {
		return nil
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	now := b.now()
	day := b.policy.DayKey(now)
	for _, a := range list {
		for _, kind := range b.policy.Due(now, a.PickupState()) {
			fresh, err := b.store.MarkReminderSent(ctx, a.ID, kind, day)
			if err != nil {
				return err
			}
			if fresh {
				b.sendReminder(kind, a)
			}
		}
	}
	return nil
}

func (b *Bot) sendReminder(kind domain.ReminderKind, a store.Assignment) {
	child := a.ChildFirstName
	switch kind {
	case domain.RemindGiverOrder:
		b.sendGiverCard(a, b.tx("remind.order", "child", child, "order_deadline", b.orderDeadlineText()))
	case domain.RemindGiverArrivalCheck:
		b.sendGiverCard(a, b.tx("remind.arrival_check", "child", child))
	case domain.RemindGiverCodePending:
		b.send(a.Giver.TgID, b.tx("remind.code_pending", "child", child), inlineButtons(button(b.label("btn.send_code"), "code", a.ID)))
	case domain.RemindGiverCodeExpired:
		b.send(a.Giver.TgID, b.tx("remind.code_expired", "child", child), inlineButtons(button(b.label("btn.send_code"), "code", a.ID)))
	case domain.RemindRecipientPickup:
		b.send(a.Parent.TgID, b.tx("remind.pickup", "child", child, "pvz", a.Parent.PVZAddress), inlineButtons(button(b.label("btn.going"), "pick", a.ID)))
	case domain.RemindRecipientCodeToday:
		b.send(a.Parent.TgID, b.tx("remind.code_today", "child", child),
			inlineButtons(button(b.label("btn.show_code"), "pick", a.ID), button(b.label("btn.received"), "got", a.ID)))
	case domain.RemindAdminStuck:
		since := ""
		if a.ArrivedAt != nil {
			since = " с " + b.localDate(*a.ArrivedAt)
		}
		b.notifyAdmins(fmt.Sprintf("⚠️ Посылка для %s %s [child %d] лежит в ПВЗ%s и не забрана.\nРодитель: %s %s%s, %s\nДаритель: %s %s%s",
			esc(child), esc(a.ChildLastName), a.ChildID, since,
			esc(a.Parent.FirstName), esc(a.Parent.LastName), usernameSuffix(a.Parent), esc(a.Parent.Phone),
			esc(a.Giver.FirstName), esc(a.Giver.LastName), usernameSuffix(a.Giver)))
	}
}
