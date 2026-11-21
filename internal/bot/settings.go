package bot

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

// Settings are the schedule and conditions of an exchange. Values never saved for an exchange
// fall back to the env defaults; nil registration dates mean organizers open and close it by hand.
type Settings struct {
	Title                string
	BudgetMax            int
	Currency             string
	RegistrationOpensAt  *time.Time
	RegistrationClosesAt *time.Time
	OrderDeadline        time.Time // zero: not set
	DeliveryBy           time.Time // zero: not set
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

// EffectiveSettings merges what was saved for the exchange with the env defaults.
func (b *Bot) EffectiveSettings(ex store.Exchange) Settings {
	s := b.defaultSettings()
	s.Title = ex.Title
	if s.Title == "" {
		s.Title = fmt.Sprintf("Обмен #%d", ex.ID)
	}
	if ex.BudgetMax != nil {
		s.BudgetMax = *ex.BudgetMax
	}
	if ex.Currency != nil {
		s.Currency = *ex.Currency
	}
	if ex.OrderDeadline != nil {
		s.OrderDeadline = *ex.OrderDeadline
	}
	if ex.DeliveryDeadline != nil {
		s.DeliveryBy = *ex.DeliveryDeadline
	}
	s.RegistrationOpensAt = ex.RegistrationOpensAt
	s.RegistrationClosesAt = ex.RegistrationClosesAt
	return s
}

// LoadSettings refreshes the cached settings of the current exchange. The bot is the only writer, so the cache stays in sync.
func (b *Bot) LoadSettings(ctx context.Context) error {
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	s := b.EffectiveSettings(ex)
	b.settings.Store(&s)
	return nil
}

func (b *Bot) Settings() Settings {
	if s := b.settings.Load(); s != nil {
		return *s
	}
	return b.defaultSettings()
}

func (b *Bot) validateSettings(s *Settings) error {
	s.Title = strings.TrimSpace(s.Title)
	s.Currency = strings.TrimSpace(s.Currency)
	switch {
	case utf8.RuneCountInString(s.Title) > 100:
		return refuse("Название: не больше 100 символов.")
	case s.BudgetMax < 1 || s.BudgetMax > 100000:
		return refuse("Бюджет должен быть от 1 до 100000.")
	case s.Currency == "" || utf8.RuneCountInString(s.Currency) > 10:
		return refuse("Валюта: от 1 до 10 символов, например BYN.")
	case s.OrderDeadline.IsZero() || s.DeliveryBy.IsZero():
		return refuse("Укажите срок отправки подарков и дату завершения.")
	case s.OrderDeadline.After(s.DeliveryBy):
		return refuse("Срок отправки подарков должен быть не позже даты завершения.")
	case (s.RegistrationOpensAt == nil) != (s.RegistrationClosesAt == nil):
		return refuse("Укажите и начало, и конец регистрации, или оставьте оба поля пустыми.")
	}
	if s.RegistrationOpensAt != nil {
		if !s.RegistrationOpensAt.Before(*s.RegistrationClosesAt) {
			return refuse("Регистрация должна закрываться позже, чем открывается.")
		}
		if !s.RegistrationClosesAt.Before(b.endOfDate(s.OrderDeadline)) {
			return refuse("Регистрация должна закрыться раньше срока отправки подарков.")
		}
	}
	return nil
}

// UpdateSettings changes the current exchange. Registration dates are frozen once registration has closed.
func (b *Bot) UpdateSettings(ctx context.Context, s Settings) error {
	if err := b.validateSettings(&s); err != nil {
		return err
	}
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	switch ex.Status {
	case domain.ExchangeCompleted:
		return refuse("Обмен завершён, его условия больше не меняются.")
	case domain.ExchangeClosed, domain.ExchangeDistributed:
		s.RegistrationOpensAt, s.RegistrationClosesAt = ex.RegistrationOpensAt, ex.RegistrationClosesAt
	case domain.ExchangeOpen:
		if s.RegistrationClosesAt != nil && !s.RegistrationClosesAt.After(b.now()) {
			return refuse("Регистрация уже идёт: дата закрытия должна быть в будущем.")
		}
	}
	if err := b.store.SaveExchangeSchedule(ctx, ex.ID, toSchedule(s)); err != nil {
		return err
	}
	return b.LoadSettings(ctx)
}

// NewExchange creates the next exchange with its schedule; the previous one must be completed.
func (b *Bot) NewExchange(ctx context.Context, s Settings) error {
	if err := b.validateSettings(&s); err != nil {
		return err
	}
	ex, err := b.store.CurrentExchange(ctx)
	if err != nil {
		return err
	}
	if ex.Status != domain.ExchangeCompleted {
		return refuse("Новый обмен можно создать только после завершения текущего.")
	}
	if _, err := b.store.CreateExchange(ctx, toSchedule(s)); err != nil {
		return err
	}
	return b.LoadSettings(ctx)
}

func toSchedule(s Settings) store.ExchangeSchedule {
	return store.ExchangeSchedule{
		Title:                s.Title,
		BudgetMax:            s.BudgetMax,
		Currency:             s.Currency,
		RegistrationOpensAt:  s.RegistrationOpensAt,
		RegistrationClosesAt: s.RegistrationClosesAt,
		OrderDeadline:        s.OrderDeadline,
		DeliveryDeadline:     s.DeliveryBy,
	}
}

// endOfDate is the first local instant after a calendar date (DATE columns come back as UTC midnight).
func (b *Bot) endOfDate(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, b.cfg.Location)
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
