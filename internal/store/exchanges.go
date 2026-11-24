package store

import (
	"context"
	"crypto/rand"
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
	InviteCode    string
	CreatedAt     time.Time
	DistributedAt *time.Time
	CompletedAt   *time.Time

	RegistrationOpensAt  *time.Time
	RegistrationClosesAt *time.Time
	BudgetMin            *int
	OrderDeadline        *time.Time
	DeliveryDeadline     *time.Time

	OrderCheckedAt    *time.Time
	DeliveryCheckedAt *time.Time
}

// ExchangeSchedule is what organizers set for an exchange; nil registration dates mean manual open/close.
type ExchangeSchedule struct {
	Title                string
	BudgetMin            int
	RegistrationOpensAt  *time.Time
	RegistrationClosesAt *time.Time
	OrderDeadline        time.Time
	DeliveryDeadline     time.Time
}

const exchangeColumns = `e.id, e.status, e.title, e.invite_code, e.created_at, e.distributed_at, e.completed_at,
	e.registration_opens_at, e.registration_closes_at, e.gift_budget_min,
	e.order_deadline, e.delivery_deadline, e.order_checked_at, e.delivery_checked_at`

func scanExchange(row interface{ Scan(...any) error }, extra ...any) (Exchange, error) {
	var e Exchange
	var distributed, completed, opens, closes, order, delivery, orderChecked, deliveryChecked sql.NullTime
	var budget sql.NullInt64
	dest := []any{&e.ID, &e.Status, &e.Title, &e.InviteCode, &e.CreatedAt, &distributed, &completed,
		&opens, &closes, &budget, &order, &delivery, &orderChecked, &deliveryChecked}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Exchange{}, err
	}
	e.DistributedAt, e.CompletedAt = nullTime(distributed), nullTime(completed)
	e.RegistrationOpensAt, e.RegistrationClosesAt = nullTime(opens), nullTime(closes)
	e.OrderDeadline, e.DeliveryDeadline = nullTime(order), nullTime(delivery)
	e.OrderCheckedAt, e.DeliveryCheckedAt = nullTime(orderChecked), nullTime(deliveryChecked)
	if budget.Valid {
		v := int(budget.Int64)
		e.BudgetMin = &v
	}
	return e, nil
}

// ActiveExchange returns the exchange that is running now: registration open, closed or gifts distributed.
// Organizers can run only one at a time. With none it returns the zero Exchange (ID 0, status ExchangeNone):
// lookups by ID 0 find nothing and no status check passes.
func (s *Store) ActiveExchange(ctx context.Context) (Exchange, error) {
	e, err := scanExchange(s.db.QueryRowContext(ctx, `SELECT `+exchangeColumns+` FROM exchanges e
		WHERE e.status IN (?, ?, ?) ORDER BY e.id DESC LIMIT 1`,
		domain.ExchangeOpen, domain.ExchangeClosed, domain.ExchangeDistributed))
	if errors.Is(err, sql.ErrNoRows) {
		return Exchange{Status: domain.ExchangeNone}, nil
	}
	if err != nil {
		return Exchange{}, fmt.Errorf("active exchange: %w", err)
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

// ListExchanges returns all exchanges, newest first.
func (s *Store) ListExchanges(ctx context.Context) ([]ExchangeSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+exchangeColumns+`,
			(SELECT COUNT(DISTINCT c.parent_user_id) FROM children c WHERE c.exchange_id = e.id AND c.deleted_at IS NULL),
			(SELECT COUNT(*) FROM children c WHERE c.exchange_id = e.id AND c.deleted_at IS NULL),
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

// CreateExchange adds a planned (draft) exchange; organizers open its registration by hand later.
func (s *Store) CreateExchange(ctx context.Context, sch ExchangeSchedule) (int64, error) {
	code, err := newInviteCode()
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO exchanges (status, title, invite_code, gift_budget_min, registration_opens_at, registration_closes_at, order_deadline, delivery_deadline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		domain.ExchangeDraft, sch.Title, code, sch.BudgetMin, utcPtr(sch.RegistrationOpensAt), utcPtr(sch.RegistrationClosesAt),
		sch.OrderDeadline.Format(time.DateOnly), sch.DeliveryDeadline.Format(time.DateOnly))
	if err != nil {
		return 0, fmt.Errorf("insert exchange: %w", err)
	}
	return res.LastInsertId()
}

func newInviteCode() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("invite code: %w", err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

func (s *Store) SaveExchangeSchedule(ctx context.Context, id int64, sch ExchangeSchedule) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE exchanges SET title = ?, gift_budget_min = ?, registration_opens_at = ?,
			registration_closes_at = ?, order_deadline = ?, delivery_deadline = ?
		WHERE id = ?`,
		sch.Title, sch.BudgetMin, utcPtr(sch.RegistrationOpensAt), utcPtr(sch.RegistrationClosesAt),
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

// DeleteExchange removes an exchange with its children, assignments, violations and reminder marks.
func (s *Store) DeleteExchange(ctx context.Context, id int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	steps := []string{
		`DELETE r FROM reminders_sent r JOIN assignments a ON a.id = r.assignment_id WHERE a.exchange_id = ?`,
		`DELETE FROM violations WHERE exchange_id = ?`,
		`DELETE FROM assignments WHERE exchange_id = ?`,
		`DELETE FROM children WHERE exchange_id = ?`,
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return false, fmt.Errorf("delete exchange data: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM exchanges WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete exchange: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, tx.Commit()
}

// ParentTgIDs lists Telegram ids of everyone who registered children in the exchange.
func (s *Store) ParentTgIDs(ctx context.Context, exchangeID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT u.tg_id FROM children c JOIN users u ON u.id = c.parent_user_id WHERE c.exchange_id = ?`, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("parent tg ids: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
