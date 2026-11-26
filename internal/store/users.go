package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID         int64
	TgID       int64
	TgUsername string
	FirstName  string
	LastName   string
	Phone      string
	PVZAddress string
	CreatedAt  time.Time
	BannedAt   *time.Time
	BanReason  string
}

const userColumns = `u.id, u.tg_id, u.tg_username, u.first_name, u.last_name, u.phone, u.pvz_address, u.created_at, u.banned_at, u.ban_reason`

func scanUser(row interface{ Scan(...any) error }, extra ...any) (User, error) {
	var u User
	var banned sql.NullTime
	dest := []any{&u.ID, &u.TgID, &u.TgUsername, &u.FirstName, &u.LastName, &u.Phone, &u.PVZAddress, &u.CreatedAt, &banned, &u.BanReason}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return User{}, err
	}
	u.BannedAt = nullTime(banned)
	return u, nil
}

func (s *Store) userWhere(ctx context.Context, where string, arg any) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users u WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("user: %w", err)
	}
	return u, nil
}

func (s *Store) UserByTG(ctx context.Context, tgID int64) (User, error) {
	return s.userWhere(ctx, "u.tg_id = ?", tgID)
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.userWhere(ctx, "u.id = ?", id)
}

func (s *Store) SaveUser(ctx context.Context, u User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (tg_id, tg_username, first_name, last_name, phone, pvz_address)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE tg_username = VALUES(tg_username), first_name = VALUES(first_name),
			last_name = VALUES(last_name), phone = VALUES(phone), pvz_address = VALUES(pvz_address)`,
		u.TgID, u.TgUsername, u.FirstName, u.LastName, u.Phone, u.PVZAddress)
	if err != nil {
		return fmt.Errorf("save user: %w", err)
	}
	return nil
}

func (s *Store) UpdateUser(ctx context.Context, id int64, u User) (bool, error) {
	return s.exec1(ctx, "update user", `
		UPDATE users SET first_name = ?, last_name = ?, phone = ?, pvz_address = ?
		WHERE id = ?`, u.FirstName, u.LastName, u.Phone, u.PVZAddress, id)
}

func (s *Store) SetBanned(ctx context.Context, userID int64, banned bool, reason string, now time.Time) (bool, error) {
	if !banned {
		return s.exec1(ctx, "unban user", `UPDATE users SET banned_at = NULL, ban_reason = '' WHERE id = ?`, userID)
	}
	return s.exec1(ctx, "ban user", `UPDATE users SET banned_at = ?, ban_reason = ? WHERE id = ?`, now.UTC(), reason, userID)
}

type Participant struct {
	User
	Children int
}

// Participants lists users who registered at least one child in the exchange.
func (s *Store) Participants(ctx context.Context, exchangeID int64) ([]Participant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userColumns+`, COUNT(c.id)
		FROM users u
		JOIN children c ON c.parent_user_id = u.id AND c.exchange_id = ? AND c.deleted_at IS NULL
		GROUP BY u.id
		ORDER BY u.last_name, u.first_name`, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("participants: %w", err)
	}
	defer rows.Close()
	var out []Participant
	for rows.Next() {
		var p Participant
		if p.User, err = scanUser(rows, &p.Children); err != nil {
			return nil, fmt.Errorf("scan participant: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	stats, err := s.UserStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]User, len(stats))
	for i, st := range stats {
		out[i] = st.User
	}
	return out, nil
}

// UserStat is one row of the organizers' user base.
type UserStat struct {
	User
	Exchanges  int
	GiftsGiven int
	Violations int
}

func (s *Store) UserStats(ctx context.Context) ([]UserStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userColumns+`,
			(SELECT COUNT(DISTINCT c.exchange_id) FROM children c WHERE c.parent_user_id = u.id AND c.deleted_at IS NULL),
			(SELECT COUNT(*) FROM assignments a WHERE a.giver_user_id = u.id),
			(SELECT COUNT(*) FROM violations v WHERE v.user_id = u.id AND v.dismissed_at IS NULL)
		FROM users u
		ORDER BY u.last_name, u.first_name`)
	if err != nil {
		return nil, fmt.Errorf("user stats: %w", err)
	}
	defer rows.Close()
	var out []UserStat
	for rows.Next() {
		var st UserStat
		if st.User, err = scanUser(rows, &st.Exchanges, &st.GiftsGiven, &st.Violations); err != nil {
			return nil, fmt.Errorf("scan user stat: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ChildWithExchange is a child row together with the exchange it was registered in.
type ChildWithExchange struct {
	Child
	ExchangeID int64
}

func (s *Store) ChildrenOfUser(ctx context.Context, parentID int64) ([]ChildWithExchange, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, parent_user_id, first_name, last_name, age, gender, wishes, exchange_id
		FROM children WHERE parent_user_id = ? AND deleted_at IS NULL ORDER BY exchange_id DESC, id`, parentID)
	if err != nil {
		return nil, fmt.Errorf("children of user: %w", err)
	}
	defer rows.Close()
	var out []ChildWithExchange
	for rows.Next() {
		var c ChildWithExchange
		if err := rows.Scan(&c.ID, &c.ParentID, &c.FirstName, &c.LastName, &c.Age, &c.Gender, &c.Wishes, &c.ExchangeID); err != nil {
			return nil, fmt.Errorf("scan child: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ChildByID(ctx context.Context, id int64) (ChildWithExchange, error) {
	var c ChildWithExchange
	err := s.db.QueryRowContext(ctx, `
		SELECT id, parent_user_id, first_name, last_name, age, gender, wishes, exchange_id
		FROM children WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&c.ID, &c.ParentID, &c.FirstName, &c.LastName, &c.Age, &c.Gender, &c.Wishes, &c.ExchangeID)
	if errors.Is(err, sql.ErrNoRows) {
		return ChildWithExchange{}, ErrNotFound
	}
	if err != nil {
		return ChildWithExchange{}, fmt.Errorf("child by id: %w", err)
	}
	return c, nil
}

// UsersByIDs returns the users that exist among ids; unknown ids are skipped.
func (s *Store) UsersByIDs(ctx context.Context, ids []int64) ([]User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("users by ids: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
