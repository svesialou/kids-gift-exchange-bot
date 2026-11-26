package store

import (
	"context"
	"fmt"
	"time"
)

// SentMessage is one message the bot delivered to a user's Telegram chat.
type SentMessage struct {
	ID        int64
	UserID    int64
	Kind      string
	Text      string
	CreatedAt time.Time
}

func (s *Store) RecordSentMessage(ctx context.Context, userID int64, kind, text string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sent_messages (user_id, kind, text)
		VALUES (?, ?, ?)`, userID, kind, text)
	if err != nil {
		return fmt.Errorf("record sent message: %w", err)
	}
	return nil
}

// SentMessagesOfUser lists the messages the bot sent to the user, newest first.
func (s *Store) SentMessagesOfUser(ctx context.Context, userID int64) ([]SentMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, kind, text, created_at
		FROM sent_messages WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("sent messages of user: %w", err)
	}
	defer rows.Close()
	var out []SentMessage
	for rows.Next() {
		var m SentMessage
		if err := rows.Scan(&m.ID, &m.UserID, &m.Kind, &m.Text, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan sent message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
