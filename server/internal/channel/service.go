package channel

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Create creates a new channel inside groupID.
func Create(db *sql.DB, groupID, name, chanType string) (*Channel, error) {
	id := uuid.New().String()
	now := time.Now()
	if chanType == "" {
		chanType = "text"
	}
	_, err := db.Exec(
		`INSERT INTO channels (id, group_id, name, type, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, groupID, name, chanType, now,
	)
	if err != nil {
		return nil, err
	}
	return &Channel{ID: id, GroupID: groupID, Name: name, Type: chanType, CreatedAt: now}, nil
}

// ListForGroup returns all channels belonging to groupID.
func ListForGroup(db *sql.DB, groupID string) ([]Channel, error) {
	rows, err := db.Query(`
		SELECT id, group_id, name, type, created_at
		FROM channels
		WHERE group_id = ?
		ORDER BY created_at ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.GroupID, &c.Name, &c.Type, &c.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

// GroupIDForChannel returns the group_id for a given channel id.
func GroupIDForChannel(db *sql.DB, channelID string) (string, error) {
	var groupID string
	err := db.QueryRow(`SELECT group_id FROM channels WHERE id = ?`, channelID).Scan(&groupID)
	return groupID, err
}
