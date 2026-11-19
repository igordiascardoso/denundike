package message

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Post inserts a new message into channelID posted by userID.
func Post(db *sql.DB, channelID, userID, text string) (*Message, error) {
	id := uuid.New().String()
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO messages (id, channel_id, user_id, text, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, channelID, userID, text, now,
	)
	if err != nil {
		return nil, err
	}
	return &Message{ID: id, ChannelID: channelID, UserID: userID, Text: text, CreatedAt: now}, nil
}

// List returns up to limit messages in channelID created before beforeID (cursor pagination).
// If beforeID is empty, the most recent messages are returned.
func List(db *sql.DB, channelID, beforeID string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if beforeID == "" {
		rows, err = db.Query(`
			SELECT id, channel_id, user_id, text, created_at
			FROM messages
			WHERE channel_id = ?
			ORDER BY created_at DESC
			LIMIT ?
		`, channelID, limit)
	} else {
		// Fetch the created_at of the cursor message first.
		var cursorTime time.Time
		if err := db.QueryRow(
			`SELECT created_at FROM messages WHERE id = ?`, beforeID,
		).Scan(&cursorTime); err != nil {
			return nil, err
		}
		rows, err = db.Query(`
			SELECT id, channel_id, user_id, text, created_at
			FROM messages
			WHERE channel_id = ? AND created_at < ?
			ORDER BY created_at DESC
			LIMIT ?
		`, channelID, cursorTime, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Text, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
