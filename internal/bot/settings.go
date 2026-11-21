package bot

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Settings are the exchange conditions shown to participants. Organizers edit them in the panel;
// values not saved for the current exchange fall back to the env defaults.
type Settings struct {
	BudgetMax     int
	Currency      string
	OrderDeadline time.Time // zero: no deadline set
	DeliveryBy    time.Time // zero: not set
}

func (b *Bot) defaultSettings() Settings {
	now := b.now().In(b.cfg.Location)
	s := Settings{BudgetMax: b.cfg.GiftBudgetMax, Currency: b.cfg.GiftCurrency}
	if d, ok := parseDayMonth(b.cfg.OrderDeadline, now); ok {
		s.OrderDeadline = d
	}
	if d, ok := parseDayMonth(b.cfg.DeliveryTarget, now); ok {
		s.DeliveryBy = d
	}
	return s
}

// LoadSettings refreshes the cached settings of the current exchange. The bot is the only writer, so the cache stays in sync.
func (b *Bot) LoadSettings(ctx context.Context) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	raw, err := b.store.ExchangeSettings(ctx, ex.ID)
	if err != nil {
		return err
	}
	s := b.defaultSettings()
	if raw.BudgetMax != nil {
		s.BudgetMax = *raw.BudgetMax
	}
	if raw.Currency != nil {
		s.Currency = *raw.Currency
	}
	if raw.OrderDeadline != nil {
		s.OrderDeadline = *raw.OrderDeadline
	}
	if raw.DeliveryDeadline != nil {
		s.DeliveryBy = *raw.DeliveryDeadline
	}
	b.settings.Store(&s)
	return nil
}

func (b *Bot) Settings() Settings {
	if s := b.settings.Load(); s != nil {
		return *s
	}
	return b.defaultSettings()
}

func (b *Bot) UpdateSettings(ctx context.Context, s Settings) error {
	s.Currency = strings.TrimSpace(s.Currency)
	switch {
	case s.BudgetMax < 1 || s.BudgetMax > 100000:
		return refuse("Бюджет должен быть от 1 до 100000.")
	case s.Currency == "" || utf8.RuneCountInString(s.Currency) > 10:
		return refuse("Валюта: от 1 до 10 символов, например BYN.")
	case s.OrderDeadline.IsZero() || s.DeliveryBy.IsZero():
		return refuse("Укажите обе даты.")
	case s.OrderDeadline.After(s.DeliveryBy):
		return refuse("Заказать подарок нужно не позже даты, к которой дети должны его получить.")
	}
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if err := b.store.SaveExchangeSettings(ctx, ex.ID, s.BudgetMax, s.Currency, s.OrderDeadline, s.DeliveryBy); err != nil {
		return err
	}
	return b.LoadSettings(ctx)
}

func (b *Bot) budgetText() string {
	s := b.Settings()
	return fmt.Sprintf("до %d %s", s.BudgetMax, s.Currency)
}

func (b *Bot) orderDeadlineText() string {
	s := b.Settings()
	if s.OrderDeadline.IsZero() {
		return "как можно скорее"
	}
	return "до " + s.OrderDeadline.Format("02.01")
}

func (b *Bot) deliveryText() string {
	s := b.Settings()
	if s.DeliveryBy.IsZero() {
		return "праздника"
	}
	return s.DeliveryBy.Format("02.01")
}
