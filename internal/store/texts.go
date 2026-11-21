package store

import (
	"context"
	"fmt"
)

// TextOverrides returns organizer-edited bot texts by key.
func (s *Store) TextOverrides(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT text_key, value FROM bot_texts`)
	if err != nil {
		return nil, fmt.Errorf("text overrides: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan text: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SaveText(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO bot_texts (text_key, value) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value)`, key, value)
	if err != nil {
		return fmt.Errorf("save text: %w", err)
	}
	return nil
}

func (s *Store) DeleteText(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bot_texts WHERE text_key = ?`, key); err != nil {
		return fmt.Errorf("delete text: %w", err)
	}
	return nil
}
