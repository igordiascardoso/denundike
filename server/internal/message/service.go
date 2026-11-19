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
			SELECT m.id, m.channel_id, m.user_id, u.login, m.text, m.created_at
			FROM messages m JOIN users u ON u.id = m.user_id
			WHERE m.channel_id = ?
			ORDER BY m.rowid DESC
			LIMIT ?
		`, channelID, limit)
	} else {
		rows, err = db.Query(`
			SELECT m.id, m.channel_id, m.user_id, u.login, m.text, m.created_at
			FROM messages m JOIN users u ON u.id = m.user_id
			WHERE m.channel_id = ? AND m.rowid < (SELECT rowid FROM messages WHERE id = ?)
			ORDER BY m.rowid DESC
			LIMIT ?
		`, channelID, beforeID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Login, &m.Text, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
