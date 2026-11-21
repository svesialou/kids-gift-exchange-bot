package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
)

// Exchange is one round of the gift exchange. Nullable settings fall back to env defaults in the bot.
type Exchange struct {
	ID            int64
	Status        domain.ExchangeStatus
	Title         string
	CreatedAt     time.Time
	DistributedAt *time.Time
	CompletedAt   *time.Time

	RegistrationOpensAt  *time.Time
	RegistrationClosesAt *time.Time
	BudgetMax            *int
	Currency             *string
	OrderDeadline        *time.Time
	DeliveryDeadline     *time.Time

	OrderCheckedAt    *time.Time
	DeliveryCheckedAt *time.Time
}

// ExchangeSchedule is what organizers set for an exchange; nil registration dates mean manual open/close.
type ExchangeSchedule struct {
	Title                string
	BudgetMax            int
	Currency             string
	RegistrationOpensAt  *time.Time
	RegistrationClosesAt *time.Time
	OrderDeadline        time.Time
	DeliveryDeadline     time.Time
}

const exchangeColumns = `e.id, e.status, e.title, e.created_at, e.distributed_at, e.completed_at,
	e.registration_opens_at, e.registration_closes_at, e.gift_budget_max, e.gift_currency,
	e.order_deadline, e.delivery_deadline, e.order_checked_at, e.delivery_checked_at`

func scanExchange(row interface{ Scan(...any) error }, extra ...any) (Exchange, error) {
	var e Exchange
	var distributed, completed, opens, closes, order, delivery, orderChecked, deliveryChecked sql.NullTime
	var budget sql.NullInt64
	var currency sql.NullString
	dest := []any{&e.ID, &e.Status, &e.Title, &e.CreatedAt, &distributed, &completed,
		&opens, &closes, &budget, &currency, &order, &delivery, &orderChecked, &deliveryChecked}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Exchange{}, err
	}
	e.DistributedAt, e.CompletedAt = nullTime(distributed), nullTime(completed)
	e.RegistrationOpensAt, e.RegistrationClosesAt = nullTime(opens), nullTime(closes)
	e.OrderDeadline, e.DeliveryDeadline = nullTime(order), nullTime(delivery)
	e.OrderCheckedAt, e.DeliveryCheckedAt = nullTime(orderChecked), nullTime(deliveryChecked)
	if budget.Valid {
		v := int(budget.Int64)
		e.BudgetMax = &v
	}
	if currency.Valid {
		e.Currency = &currency.String
	}
	return e, nil
}

// CurrentExchange returns the latest exchange, creating a draft one on first run.
func (s *Store) CurrentExchange(ctx context.Context) (Exchange, error) {
	e, err := scanExchange(s.db.QueryRowContext(ctx, `SELECT `+exchangeColumns+` FROM exchanges e ORDER BY e.id DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO exchanges (status) VALUES (?)`, domain.ExchangeDraft); err != nil {
			return Exchange{}, fmt.Errorf("create exchange: %w", err)
		}
		return s.CurrentExchange(ctx)
	}
	if err != nil {
		return Exchange{}, fmt.Errorf("current exchange: %w", err)
	}
	return e, nil
}

func (s *Store) ExchangeByID(ctx context.Context, id int64) (Exchange, error) {
	e, err := scanExchange(s.db.QueryRowContext(ctx, `SELECT `+exchangeColumns+` FROM exchanges e WHERE e.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Exchange{}, ErrNotFound
	}
	if err != nil {
		return Exchange{}, fmt.Errorf("exchange by id: %w", err)
	}
	return e, nil
}

type ExchangeSummary struct {
	Exchange
	Families   int
	Children   int
	Gifts      int
	Received   int
	Violations int
}

func (s *Store) ListExchanges(ctx context.Context) ([]ExchangeSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+exchangeColumns+`,
			(SELECT COUNT(DISTINCT c.parent_user_id) FROM children c WHERE c.exchange_id = e.id),
			(SELECT COUNT(*) FROM children c WHERE c.exchange_id = e.id),
			(SELECT COUNT(*) FROM assignments a WHERE a.exchange_id = e.id),
			(SELECT COUNT(*) FROM assignments a WHERE a.exchange_id = e.id AND a.status = ?),
			(SELECT COUNT(*) FROM violations v WHERE v.exchange_id = e.id AND v.dismissed_at IS NULL)
		FROM exchanges e ORDER BY e.id DESC`, domain.AssignmentReceived)
	if err != nil {
		return nil, fmt.Errorf("list exchanges: %w", err)
	}
	defer rows.Close()
	var out []ExchangeSummary
	for rows.Next() {
		var x ExchangeSummary
		e, err := scanExchange(rows, &x.Families, &x.Children, &x.Gifts, &x.Received, &x.Violations)
		if err != nil {
			return nil, fmt.Errorf("scan exchange: %w", err)
		}
		x.Exchange = e
		out = append(out, x)
	}
	return out, rows.Err()
}

// CreateExchange starts a new draft exchange; only allowed when the latest one is completed.
func (s *Store) CreateExchange(ctx context.Context, sch ExchangeSchedule) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status domain.ExchangeStatus
	err = tx.QueryRowContext(ctx, `SELECT status FROM exchanges ORDER BY id DESC LIMIT 1 FOR UPDATE`).Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("lock latest exchange: %w", err)
	}
	if err == nil && status != domain.ExchangeCompleted {
		return 0, fmt.Errorf("latest exchange is %s, want %s", status, domain.ExchangeCompleted)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO exchanges (status, title, gift_budget_max, gift_currency, registration_opens_at, registration_closes_at, order_deadline, delivery_deadline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		domain.ExchangeDraft, sch.Title, sch.BudgetMax, sch.Currency, utcPtr(sch.RegistrationOpensAt), utcPtr(sch.RegistrationClosesAt),
		sch.OrderDeadline.Format(time.DateOnly), sch.DeliveryDeadline.Format(time.DateOnly))
	if err != nil {
		return 0, fmt.Errorf("insert exchange: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

func (s *Store) SaveExchangeSchedule(ctx context.Context, id int64, sch ExchangeSchedule) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE exchanges SET title = ?, gift_budget_max = ?, gift_currency = ?, registration_opens_at = ?,
			registration_closes_at = ?, order_deadline = ?, delivery_deadline = ?
		WHERE id = ?`,
		sch.Title, sch.BudgetMax, sch.Currency, utcPtr(sch.RegistrationOpensAt), utcPtr(sch.RegistrationClosesAt),
		sch.OrderDeadline.Format(time.DateOnly), sch.DeliveryDeadline.Format(time.DateOnly), id)
	if err != nil {
		return fmt.Errorf("save exchange schedule: %w", err)
	}
	return nil
}

// TransitionExchange moves the exchange to `to` only if it is currently in one of `from`.
func (s *Store) TransitionExchange(ctx context.Context, id int64, to domain.ExchangeStatus, from ...domain.ExchangeStatus) (bool, error) {
	query := `UPDATE exchanges SET status = ?, completed_at = IF(? = 'completed', UTC_TIMESTAMP(), completed_at) WHERE id = ? AND status IN (` + placeholders(len(from)) + `)`
	args := []any{to, to, id}
	for _, f := range from {
		args = append(args, f)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("transition exchange: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func utcPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
