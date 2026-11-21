package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
	"github.com/svesialou/kids-gift-exchange-bot/internal/store"
)

// Settings are the schedule and conditions of an exchange. Organizers fill every field when creating it.
type Settings struct {
	Title                string
	BudgetMax            int
	Currency             string
	RegistrationOpensAt  *time.Time
	RegistrationClosesAt *time.Time
	OrderDeadline        time.Time
	DeliveryBy           time.Time
}

// EffectiveSettings reads the settings saved for an exchange; missing values stay zero.
func (b *Bot) EffectiveSettings(ex store.Exchange) Settings {
	s := Settings{Title: ex.Title, RegistrationOpensAt: ex.RegistrationOpensAt, RegistrationClosesAt: ex.RegistrationClosesAt}
	if s.Title == "" && ex.ID != 0 {
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
	return s
}

// LoadSettings refreshes the cached settings of the active exchange used in chat texts.
// The bot is the only writer, so the cache stays in sync.
func (b *Bot) LoadSettings(ctx context.Context) error {
	ex, err := b.store.ActiveExchange(ctx)
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
	return Settings{}
}

func (b *Bot) validateSettings(s *Settings) error {
	s.Title = strings.TrimSpace(s.Title)
	s.Currency = strings.TrimSpace(s.Currency)
	switch {
	case s.Title == "" || utf8.RuneCountInString(s.Title) > 100:
		return refuse("Название: от 1 до 100 символов.")
	case s.BudgetMax < 1 || s.BudgetMax > 100000:
		return refuse("Бюджет должен быть от 1 до 100000.")
	case s.Currency == "" || utf8.RuneCountInString(s.Currency) > 10:
		return refuse("Валюта: от 1 до 10 символов, например BYN.")
	case s.RegistrationOpensAt == nil || s.RegistrationClosesAt == nil:
		return refuse("Укажите начало и конец регистрации.")
	case s.OrderDeadline.IsZero() || s.DeliveryBy.IsZero():
		return refuse("Укажите срок отправки подарков и дату завершения.")
	case !s.RegistrationOpensAt.Before(*s.RegistrationClosesAt):
		return refuse("Регистрация должна закрываться позже, чем открывается.")
	case !s.RegistrationClosesAt.Before(b.endOfDate(s.OrderDeadline)):
		return refuse("Регистрация должна закрыться раньше срока отправки подарков.")
	case s.OrderDeadline.After(s.DeliveryBy):
		return refuse("Срок отправки подарков должен быть не позже даты завершения.")
	}
	return nil
}

// NewExchange adds a planned exchange. Several can be planned; only one runs at a time.
func (b *Bot) NewExchange(ctx context.Context, s Settings) (int64, error) {
	if err := b.validateSettings(&s); err != nil {
		return 0, err
	}
	if !s.RegistrationClosesAt.After(b.now()) {
		return 0, refuse("Конец регистрации уже прошёл, укажите дату в будущем.")
	}
	return b.store.CreateExchange(ctx, toSchedule(s))
}

// UpdateSettings changes an exchange. Registration dates are frozen once registration has closed.
func (b *Bot) UpdateSettings(ctx context.Context, id int64, s Settings) error {
	if err := b.validateSettings(&s); err != nil {
		return err
	}
	ex, err := b.exchangeByID(ctx, id)
	if err != nil {
		return err
	}
	switch ex.Status {
	case domain.ExchangeCompleted:
		return refuse("Обмен завершён, его условия больше не меняются.")
	case domain.ExchangeClosed, domain.ExchangeDistributed:
		s.RegistrationOpensAt, s.RegistrationClosesAt = ex.RegistrationOpensAt, ex.RegistrationClosesAt
	case domain.ExchangeOpen:
		if !s.RegistrationClosesAt.After(b.now()) {
			return refuse("Регистрация уже идёт: дата закрытия должна быть в будущем.")
		}
	}
	if err := b.store.SaveExchangeSchedule(ctx, ex.ID, toSchedule(s)); err != nil {
		return err
	}
	return b.LoadSettings(ctx)
}

func (b *Bot) exchangeByID(ctx context.Context, id int64) (store.Exchange, error) {
	ex, err := b.store.ExchangeByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ex, refuse("Обмен не найден.")
	}
	return ex, err
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
