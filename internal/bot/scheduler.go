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
	ex, err := b.store.CurrentExchange(ctx)
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
	child := esc(a.ChildFirstName)
	switch kind {
	case domain.RemindGiverOrder:
		b.sendGiverCard(a, fmt.Sprintf("⏰ Напоминание: подарок для <b>%s</b> нужно заказать %s.", child, b.orderDeadlineText()))
	case domain.RemindGiverArrivalCheck:
		b.sendGiverCard(a, fmt.Sprintf("📦 Посылка для <b>%s</b> уже пришла в ПВЗ? Если да, нажми «Пришло в ПВЗ».", child))
	case domain.RemindGiverCodePending:
		b.send(a.Giver.TgID, fmt.Sprintf("🔔 Получатель ждёт код, чтобы забрать посылку для <b>%s</b>. Пришли сегодняшний код из приложения Wildberries.", child),
			inlineButtons(button("🔑 Отправить код получения", "code", a.ID)))
	case domain.RemindGiverCodeExpired:
		b.send(a.Giver.TgID, fmt.Sprintf("Вчерашний код для посылки <b>%s</b> не использовали, посылка всё ещё в ПВЗ. Когда получатель снова попросит, пришли новый код.", child),
			inlineButtons(button("🔑 Отправить код получения", "code", a.ID)))
	case domain.RemindRecipientPickup:
		b.send(a.Parent.TgID, fmt.Sprintf("📦 Посылка для <b>%s</b> ждёт в ПВЗ:\n%s\n\nКогда соберёшься забирать, нажми «Иду забирать», и даритель пришлёт код на сегодня.",
			child, esc(a.Parent.PVZAddress)),
			inlineButtons(button("🚶 Иду забирать, нужен код", "pick", a.ID)))
	case domain.RemindRecipientCodeToday:
		b.send(a.Parent.TgID, fmt.Sprintf("⏳ Код для посылки <b>%s</b> действует только до конца дня. Не успеваешь? Завтра нажми «Иду забирать», и даритель пришлёт новый.", child),
			inlineButtons(button("🔑 Показать код", "pick", a.ID), button("✅ Забрал(а)", "got", a.ID)))
	case domain.RemindAdminStuck:
		since := ""
		if a.ArrivedAt != nil {
			since = " с " + b.localDate(*a.ArrivedAt)
		}
		b.notifyAdmins(fmt.Sprintf("⚠️ Посылка для %s %s [child %d] лежит в ПВЗ%s и не забрана.\nРодитель: %s %s%s, %s\nДаритель: %s %s%s",
			child, esc(a.ChildLastName), a.ChildID, since,
			esc(a.Parent.FirstName), esc(a.Parent.LastName), usernameSuffix(a.Parent), esc(a.Parent.Phone),
			esc(a.Giver.FirstName), esc(a.Giver.LastName), usernameSuffix(a.Giver)))
	}
}
