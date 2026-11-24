package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/svesialou/kids-gift-exchange-bot/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies pending migrations and returns the resulting schema version.
func (s *Store) Migrate() (uint, error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return 0, fmt.Errorf("migrations source: %w", err)
	}
	driver, err := migratemysql.WithInstance(s.db, &migratemysql.Config{})
	if err != nil {
		return 0, fmt.Errorf("migrations driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "mysql", driver)
	if err != nil {
		return 0, fmt.Errorf("migrations init: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return 0, fmt.Errorf("migrations up: %w", err)
	}
	version, _, err := m.Version()
	if err != nil {
		return 0, fmt.Errorf("migrations version: %w", err)
	}
	return version, nil
}

// ---------- children ----------

type Child struct {
	ID        int64
	ParentID  int64
	FirstName string
	LastName  string
	Age       int
	Gender    string
	Wishes    string
}

func (s *Store) AddChild(ctx context.Context, exchangeID int64, c Child) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO children (exchange_id, parent_user_id, first_name, last_name, age, gender, wishes)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		exchangeID, c.ParentID, c.FirstName, c.LastName, c.Age, c.Gender, c.Wishes)
	if err != nil {
		return fmt.Errorf("add child: %w", err)
	}
	return nil
}

// DeleteChild removes a child only while registration is open, so assigned children cannot disappear.
func (s *Store) DeleteChild(ctx context.Context, exchangeID, parentID, childID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE c FROM children c
		JOIN exchanges e ON e.id = c.exchange_id
		WHERE c.id = ? AND c.parent_user_id = ? AND c.exchange_id = ? AND e.status = ?`,
		childID, parentID, exchangeID, domain.ExchangeOpen)
	if err != nil {
		return false, fmt.Errorf("delete child: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) SoftDeleteChild(ctx context.Context, id int64) (bool, error) {
	return s.exec1(ctx, "soft delete child", `
		UPDATE children SET deleted_at = ?
		WHERE id = ? AND deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM assignments WHERE child_id = ?)`, time.Now().UTC(), id, id)
}

func (s *Store) UpdateChild(ctx context.Context, id int64, c Child) (bool, error) {
	return s.exec1(ctx, "update child", `
		UPDATE children SET first_name = ?, last_name = ?, age = ?, gender = ?, wishes = ?
		WHERE id = ? AND deleted_at IS NULL`,
		c.FirstName, c.LastName, c.Age, c.Gender, c.Wishes, id)
}

func (s *Store) ChildHasAssignment(ctx context.Context, id int64) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM assignments WHERE child_id = ?)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("child has assignment: %w", err)
	}
	return exists, nil
}

func (s *Store) ChildrenOf(ctx context.Context, exchangeID, parentID int64) ([]Child, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, parent_user_id, first_name, last_name, age, gender, wishes
		FROM children WHERE exchange_id = ? AND parent_user_id = ? AND deleted_at IS NULL ORDER BY id`, exchangeID, parentID)
	if err != nil {
		return nil, fmt.Errorf("children of: %w", err)
	}
	defer rows.Close()
	var out []Child
	for rows.Next() {
		var c Child
		if err := rows.Scan(&c.ID, &c.ParentID, &c.FirstName, &c.LastName, &c.Age, &c.Gender, &c.Wishes); err != nil {
			return nil, fmt.Errorf("scan child: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ExchangeChildren lists all children of the exchange ordered by parent.
func (s *Store) ExchangeChildren(ctx context.Context, exchangeID int64) ([]Child, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, parent_user_id, first_name, last_name, age, gender, wishes
		FROM children WHERE exchange_id = ? AND deleted_at IS NULL ORDER BY parent_user_id, id`, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("exchange children: %w", err)
	}
	defer rows.Close()
	var out []Child
	for rows.Next() {
		var c Child
		if err := rows.Scan(&c.ID, &c.ParentID, &c.FirstName, &c.LastName, &c.Age, &c.Gender, &c.Wishes); err != nil {
			return nil, fmt.Errorf("scan child: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ChildRefs(ctx context.Context, exchangeID int64) ([]domain.ChildRef, error) {
	return s.childRefs(ctx, s.db, exchangeID, false)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) childRefs(ctx context.Context, q querier, exchangeID int64, forUpdate bool) ([]domain.ChildRef, error) {
	// Children of banned parents stay registered but are left out of the distribution.
	query := `SELECT c.id, c.parent_user_id FROM children c JOIN users u ON u.id = c.parent_user_id
		WHERE c.exchange_id = ? AND u.banned_at IS NULL AND c.deleted_at IS NULL ORDER BY c.id`
	if forUpdate {
		query += ` FOR UPDATE OF c`
	}
	rows, err := q.QueryContext(ctx, query, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("child refs: %w", err)
	}
	defer rows.Close()
	var out []domain.ChildRef
	for rows.Next() {
		var r domain.ChildRef
		if err := rows.Scan(&r.ChildID, &r.ParentID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Distribute locks the exchange, computes pairs with `plan` and stores them.
// It succeeds only once: the exchange must be in registration_closed.
func (s *Store) Distribute(ctx context.Context, exchangeID int64, plan func([]domain.ChildRef) ([]domain.Pair, error)) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status domain.ExchangeStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exchanges WHERE id = ? FOR UPDATE`, exchangeID).Scan(&status); err != nil {
		return 0, fmt.Errorf("lock exchange: %w", err)
	}
	if status != domain.ExchangeClosed {
		return 0, fmt.Errorf("exchange is %s, want %s", status, domain.ExchangeClosed)
	}

	refs, err := s.childRefs(ctx, tx, exchangeID, true)
	if err != nil {
		return 0, err
	}
	pairs, err := plan(refs)
	if err != nil {
		return 0, err
	}

	if len(pairs) > 0 {
		args := make([]any, 0, len(pairs)*4)
		for _, p := range pairs {
			args = append(args, exchangeID, p.ChildID, p.GiverID, domain.AssignmentAssigned)
		}
		values := "(?, ?, ?, ?)"
		for i := 1; i < len(pairs); i++ {
			values += ", (?, ?, ?, ?)"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO assignments (exchange_id, child_id, giver_user_id, status) VALUES `+values, args...); err != nil {
			return 0, fmt.Errorf("insert assignments: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE exchanges SET status = ?, distributed_at = UTC_TIMESTAMP() WHERE id = ?`,
		domain.ExchangeDistributed, exchangeID); err != nil {
		return 0, fmt.Errorf("mark distributed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(pairs), nil
}

// ---------- assignments ----------

type Assignment struct {
	ID         int64
	ExchangeID int64
	Status     domain.AssignmentStatus

	ChildID        int64
	ChildFirstName string
	ChildLastName  string
	ChildAge       int
	ChildGender    string
	ChildWishes    string

	Parent User
	Giver  User

	ExpectedDate    *time.Time
	OrderedAt       *time.Time
	ArrivedAt       *time.Time
	ReceivedAt      *time.Time
	CodeText        string
	CodeFileID      string
	CodeSentAt      *time.Time
	CodeValidUntil  *time.Time
	CodeRequestedAt *time.Time
}

func (a Assignment) PickupState() domain.PickupState {
	return domain.PickupState{
		Status:          a.Status,
		ExpectedDate:    a.ExpectedDate,
		ArrivedAt:       a.ArrivedAt,
		CodeSentAt:      a.CodeSentAt,
		CodeValidUntil:  a.CodeValidUntil,
		CodeRequestedAt: a.CodeRequestedAt,
	}
}

const assignmentSelect = `
	SELECT a.id, a.exchange_id, a.status,
		c.id, c.first_name, c.last_name, c.age, c.gender, c.wishes,
		p.id, p.tg_id, p.tg_username, p.first_name, p.last_name, p.phone, p.pvz_address,
		g.id, g.tg_id, g.tg_username, g.first_name, g.last_name, g.phone, g.pvz_address,
		a.expected_date, a.ordered_at, a.arrived_at, a.received_at,
		a.code_text, a.code_file_id, a.code_sent_at, a.code_valid_until, a.code_requested_at
	FROM assignments a
	JOIN children c ON c.id = a.child_id
	JOIN users p ON p.id = c.parent_user_id
	JOIN users g ON g.id = a.giver_user_id`

func scanAssignment(row interface{ Scan(...any) error }) (Assignment, error) {
	var a Assignment
	var expected, ordered, arrived, received, codeSent, codeValid, codeReq sql.NullTime
	err := row.Scan(&a.ID, &a.ExchangeID, &a.Status,
		&a.ChildID, &a.ChildFirstName, &a.ChildLastName, &a.ChildAge, &a.ChildGender, &a.ChildWishes,
		&a.Parent.ID, &a.Parent.TgID, &a.Parent.TgUsername, &a.Parent.FirstName, &a.Parent.LastName, &a.Parent.Phone, &a.Parent.PVZAddress,
		&a.Giver.ID, &a.Giver.TgID, &a.Giver.TgUsername, &a.Giver.FirstName, &a.Giver.LastName, &a.Giver.Phone, &a.Giver.PVZAddress,
		&expected, &ordered, &arrived, &received,
		&a.CodeText, &a.CodeFileID, &codeSent, &codeValid, &codeReq)
	if err != nil {
		return Assignment{}, err
	}
	a.ExpectedDate = nullTime(expected)
	a.OrderedAt = nullTime(ordered)
	a.ArrivedAt = nullTime(arrived)
	a.ReceivedAt = nullTime(received)
	a.CodeSentAt = nullTime(codeSent)
	a.CodeValidUntil = nullTime(codeValid)
	a.CodeRequestedAt = nullTime(codeReq)
	return a, nil
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func (s *Store) listAssignments(ctx context.Context, where string, args ...any) ([]Assignment, error) {
	rows, err := s.db.QueryContext(ctx, assignmentSelect+" WHERE "+where+" ORDER BY a.id", args...)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan assignment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Assignment(ctx context.Context, id int64) (Assignment, error) {
	a, err := scanAssignment(s.db.QueryRowContext(ctx, assignmentSelect+" WHERE a.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, ErrNotFound
	}
	if err != nil {
		return Assignment{}, fmt.Errorf("assignment: %w", err)
	}
	return a, nil
}

func (s *Store) AssignmentsByGiver(ctx context.Context, exchangeID, giverID int64) ([]Assignment, error) {
	return s.listAssignments(ctx, "a.exchange_id = ? AND a.giver_user_id = ?", exchangeID, giverID)
}

func (s *Store) AssignmentsByParent(ctx context.Context, exchangeID, parentID int64) ([]Assignment, error) {
	return s.listAssignments(ctx, "a.exchange_id = ? AND c.parent_user_id = ?", exchangeID, parentID)
}

// AssignmentsOfUser lists every gift the user gave or their children received, across all exchanges.
func (s *Store) AssignmentsOfUser(ctx context.Context, userID int64) ([]Assignment, error) {
	return s.listAssignments(ctx, "a.giver_user_id = ? OR c.parent_user_id = ?", userID, userID)
}

func (s *Store) AssignmentsByExchange(ctx context.Context, exchangeID int64) ([]Assignment, error) {
	return s.listAssignments(ctx, "a.exchange_id = ?", exchangeID)
}

func (s *Store) exec1(ctx context.Context, op, query string, args ...any) (bool, error) {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", op, err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkOrdered is allowed for the giver while the gift is not ordered yet.
func (s *Store) MarkOrdered(ctx context.Context, id, giverID int64, expected *time.Time, now time.Time) (bool, error) {
	var exp any
	if expected != nil {
		exp = expected.Format("2006-01-02")
	}
	return s.exec1(ctx, "mark ordered", `
		UPDATE assignments SET status = ?, expected_date = ?, ordered_at = ?
		WHERE id = ? AND giver_user_id = ? AND status = ?`,
		domain.AssignmentOrdered, exp, now.UTC(), id, giverID, domain.AssignmentAssigned)
}

func (s *Store) MarkArrived(ctx context.Context, id, giverID int64, now time.Time) (bool, error) {
	return s.exec1(ctx, "mark arrived", `
		UPDATE assignments SET status = ?, arrived_at = ?
		WHERE id = ? AND giver_user_id = ? AND status IN (?, ?)`,
		domain.AssignmentArrived, now.UTC(), id, giverID, domain.AssignmentAssigned, domain.AssignmentOrdered)
}

// SetCode stores a fresh pickup code. Sending a code implies the parcel has arrived.
func (s *Store) SetCode(ctx context.Context, id, giverID int64, text, fileID string, now, validUntil time.Time) (bool, error) {
	return s.exec1(ctx, "set code", `
		UPDATE assignments SET status = ?, arrived_at = COALESCE(arrived_at, ?),
			code_text = ?, code_file_id = ?, code_sent_at = ?, code_valid_until = ?
		WHERE id = ? AND giver_user_id = ? AND status IN (?, ?, ?)`,
		domain.AssignmentArrived, now.UTC(), text, fileID, now.UTC(), validUntil.UTC(),
		id, giverID, domain.AssignmentAssigned, domain.AssignmentOrdered, domain.AssignmentArrived)
}

// RequestCode records that the recipient needs a code. invalidate drops the current code ("code did not work").
func (s *Store) RequestCode(ctx context.Context, id, parentID int64, invalidate bool, now time.Time) (bool, error) {
	extra := ""
	if invalidate {
		extra = ", a.code_valid_until = NULL"
	}
	return s.exec1(ctx, "request code", `
		UPDATE assignments a JOIN children c ON c.id = a.child_id
		SET a.code_requested_at = ?`+extra+`
		WHERE a.id = ? AND c.parent_user_id = ? AND a.status = ?`,
		now.UTC(), id, parentID, domain.AssignmentArrived)
}

func (s *Store) MarkReceived(ctx context.Context, id, parentID int64, now time.Time) (bool, error) {
	return s.exec1(ctx, "mark received", `
		UPDATE assignments a JOIN children c ON c.id = a.child_id
		SET a.status = ?, a.received_at = ?
		WHERE a.id = ? AND c.parent_user_id = ? AND a.status IN (?, ?)`,
		domain.AssignmentReceived, now.UTC(), id, parentID, domain.AssignmentOrdered, domain.AssignmentArrived)
}

// Reassign hands a not yet received gift to another giver and resets its progress.
func (s *Store) Reassign(ctx context.Context, exchangeID, childID, newGiverID int64) (bool, error) {
	return s.exec1(ctx, "reassign", `
		UPDATE assignments a JOIN children c ON c.id = a.child_id
		SET a.giver_user_id = ?, a.status = ?, a.expected_date = NULL, a.ordered_at = NULL, a.arrived_at = NULL,
			a.code_text = '', a.code_file_id = '', a.code_sent_at = NULL, a.code_valid_until = NULL, a.code_requested_at = NULL
		WHERE a.exchange_id = ? AND a.child_id = ? AND a.status <> ? AND c.parent_user_id <> ?`,
		newGiverID, domain.AssignmentAssigned, exchangeID, childID, domain.AssignmentReceived, newGiverID)
}

// MarkReminderSent returns true when this reminder was not sent yet for the day.
func (s *Store) MarkReminderSent(ctx context.Context, assignmentID int64, kind domain.ReminderKind, day string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO reminders_sent (assignment_id, kind, sent_on) VALUES (?, ?, ?)`,
		assignmentID, kind, day)
	if err != nil {
		return false, fmt.Errorf("mark reminder: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---------- sessions ----------

type Session struct {
	State string
	Data  map[string]string
}

func (s *Store) Session(ctx context.Context, tgID int64) (Session, error) {
	var state, raw string
	err := s.db.QueryRowContext(ctx, `SELECT state, data FROM sessions WHERE tg_id = ?`, tgID).Scan(&state, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{Data: map[string]string{}}, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("session: %w", err)
	}
	sess := Session{State: state, Data: map[string]string{}}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &sess.Data); err != nil {
			return Session{}, fmt.Errorf("decode session: %w", err)
		}
	}
	return sess, nil
}

func (s *Store) SaveSession(ctx context.Context, tgID int64, sess Session) error {
	if sess.State == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE tg_id = ?`, tgID)
		if err != nil {
			return fmt.Errorf("clear session: %w", err)
		}
		return nil
	}
	raw, err := json.Marshal(sess.Data)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (tg_id, state, data) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE state = VALUES(state), data = VALUES(data)`, tgID, sess.State, string(raw))
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	out := "?"
	for i := 1; i < n; i++ {
		out += ", ?"
	}
	return out
}
