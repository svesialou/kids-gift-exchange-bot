package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
)

type Violation struct {
	ID            int64
	ExchangeID    int64
	ExchangeTitle string
	UserID        int64
	UserName      string
	AssignmentID  *int64
	ChildName     string
	Kind          domain.ViolationKind
	Comment       string
	CreatedAt     time.Time
	DismissedAt   *time.Time
}

// RecordOrderViolations runs once per exchange: givers who had not ordered by the deadline get an order_overdue violation.
func (s *Store) RecordOrderViolations(ctx context.Context, exchangeID int64, now time.Time) (int, error) {
	return s.recordOnce(ctx, exchangeID, now, "order_checked_at", domain.ViolationOrderOverdue,
		`a.ordered_at IS NULL AND a.arrived_at IS NULL AND a.received_at IS NULL`)
}

// RecordDeliveryViolations runs once per exchange: gifts that never reached the pickup point get a delivery_overdue violation.
func (s *Store) RecordDeliveryViolations(ctx context.Context, exchangeID int64, now time.Time) (int, error) {
	return s.recordOnce(ctx, exchangeID, now, "delivery_checked_at", domain.ViolationDeliveryOverdue,
		`a.arrived_at IS NULL AND a.received_at IS NULL`)
}

// recordOnce flips the exchange's check column and inserts violations in one transaction, so a check never runs twice.
// column and condition are constants from the callers above, never user input.
func (s *Store) recordOnce(ctx context.Context, exchangeID int64, now time.Time, column string, kind domain.ViolationKind, condition string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `UPDATE exchanges SET `+column+` = ? WHERE id = ? AND `+column+` IS NULL`, now.UTC(), exchangeID)
	if err != nil {
		return 0, fmt.Errorf("mark %s: %w", column, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, nil
	}
	res, err = tx.ExecContext(ctx, `
		INSERT IGNORE INTO violations (exchange_id, user_id, assignment_id, kind)
		SELECT a.exchange_id, a.giver_user_id, a.id, ? FROM assignments a
		WHERE a.exchange_id = ? AND `+condition, kind, exchangeID)
	if err != nil {
		return 0, fmt.Errorf("record %s: %w", kind, err)
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

func (s *Store) AddManualViolation(ctx context.Context, exchangeID, userID int64, comment string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO violations (exchange_id, user_id, kind, comment) VALUES (?, ?, ?, ?)`,
		exchangeID, userID, domain.ViolationManual, comment)
	if err != nil {
		return fmt.Errorf("add violation: %w", err)
	}
	return nil
}

func (s *Store) SetViolationDismissed(ctx context.Context, id int64, dismissed bool, now time.Time) (bool, error) {
	var at any
	if dismissed {
		at = now.UTC()
	}
	return s.exec1(ctx, "dismiss violation", `UPDATE violations SET dismissed_at = ? WHERE id = ?`, at, id)
}

const violationSelect = `
	SELECT v.id, v.exchange_id, e.title, v.user_id, CONCAT(u.first_name, ' ', u.last_name), v.assignment_id,
		COALESCE(CONCAT(c.first_name, ' ', c.last_name), ''), v.kind, v.comment, v.created_at, v.dismissed_at
	FROM violations v
	JOIN exchanges e ON e.id = v.exchange_id
	JOIN users u ON u.id = v.user_id
	LEFT JOIN assignments a ON a.id = v.assignment_id
	LEFT JOIN children c ON c.id = a.child_id`

func (s *Store) ViolationsByExchange(ctx context.Context, exchangeID int64) ([]Violation, error) {
	return s.listViolations(ctx, "v.exchange_id = ?", exchangeID)
}

func (s *Store) ViolationsByUser(ctx context.Context, userID int64) ([]Violation, error) {
	return s.listViolations(ctx, "v.user_id = ?", userID)
}

func (s *Store) listViolations(ctx context.Context, where string, args ...any) ([]Violation, error) {
	rows, err := s.db.QueryContext(ctx, violationSelect+" WHERE "+where+" ORDER BY v.id", args...)
	if err != nil {
		return nil, fmt.Errorf("list violations: %w", err)
	}
	defer rows.Close()
	var out []Violation
	for rows.Next() {
		var v Violation
		var assignment sql.NullInt64
		var dismissed sql.NullTime
		if err := rows.Scan(&v.ID, &v.ExchangeID, &v.ExchangeTitle, &v.UserID, &v.UserName, &assignment,
			&v.ChildName, &v.Kind, &v.Comment, &v.CreatedAt, &dismissed); err != nil {
			return nil, fmt.Errorf("scan violation: %w", err)
		}
		if assignment.Valid {
			id := assignment.Int64
			v.AssignmentID = &id
		}
		v.DismissedAt = nullTime(dismissed)
		out = append(out, v)
	}
	return out, rows.Err()
}
