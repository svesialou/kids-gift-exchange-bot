package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

// advanceSchedule moves the current exchange along its dates. Each step is a conditional status update,
// so repeated ticks or a restart never run a step twice; a bot that was down catches up one step per tick.
func (b *Bot) advanceSchedule(ctx context.Context, now time.Time) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	s := b.EffectiveSettings(ex)

	switch ex.Status {
	case domain.ExchangeDraft:
		if s.RegistrationOpensAt == nil || now.Before(*s.RegistrationOpensAt) {
			return nil
		}
		ok, err := b.store.TransitionExchange(ctx, ex.ID, domain.ExchangeOpen, domain.ExchangeDraft)
		if err != nil || !ok {
			return err
		}
		b.notifyAdmins(fmt.Sprintf("📅 %s: регистрация открыта по расписанию, закроется %s.\nПриглашение можно отправить из панели.",
			esc(s.Title), b.localDateTime(*s.RegistrationClosesAt)))

	case domain.ExchangeOpen:
		if s.RegistrationClosesAt == nil || now.Before(*s.RegistrationClosesAt) {
			return nil
		}
		ok, err := b.store.TransitionExchange(ctx, ex.ID, domain.ExchangeClosed, domain.ExchangeOpen)
		if err != nil || !ok {
			return err
		}
		n, err := b.Distribute(ctx)
		var ae *ActionError
		switch {
		case errors.As(err, &ae):
			b.notifyAdmins(fmt.Sprintf("⚠️ %s: регистрация закрыта по расписанию, но распределить не получилось.\n%s\nПоправьте состав в панели и распределите вручную.",
				esc(s.Title), esc(ae.Msg)))
		case err != nil:
			return err
		default:
			slog.Info("scheduled distribution done", "exchange_id", ex.ID, "gifts", n)
		}

	case domain.ExchangeDistributed:
		if !s.OrderDeadline.IsZero() && ex.OrderCheckedAt == nil && !now.Before(b.endOfDate(s.OrderDeadline)) {
			n, err := b.store.RecordOrderViolations(ctx, ex.ID, now)
			if err != nil {
				return err
			}
			b.notifyAdmins(fmt.Sprintf("⏰ %s: срок отправки подарков прошёл. Не отправили вовремя: %d. Подробности в панели, вкладка «Нарушения».",
				esc(s.Title), n))
		}
		if !s.DeliveryBy.IsZero() && !now.Before(b.endOfDate(s.DeliveryBy)) {
			if _, err := b.ChangeExchangeStatus(ctx, "finish"); err != nil {
				return err
			}
		}
	}
	return nil
}

// onExchangeCompleted records gifts that never reached the pickup point and reports to admins.
func (b *Bot) onExchangeCompleted(ctx context.Context, ex store.Exchange) error {
	n, err := b.store.RecordDeliveryViolations(ctx, ex.ID, b.now())
	if err != nil {
		return err
	}
	list, err := b.store.AssignmentsByExchange(ctx, ex.ID)
	if err != nil {
		return err
	}
	received := 0
	for _, a := range list {
		if a.Status == domain.AssignmentReceived {
			received++
		}
	}
	b.notifyAdmins(fmt.Sprintf("🏁 %s завершён. Получено подарков: %d из %d, не доставлено к завершению: %d.",
		esc(b.EffectiveSettings(ex).Title), received, len(list), n))
	return nil
}

func (b *Bot) localDateTime(t time.Time) string {
	return t.In(b.cfg.Location).Format("02.01 15:04")
}
