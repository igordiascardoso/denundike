package group

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Create creates a new group and adds the owner as the first member.
func Create(db *sql.DB, ownerID, name string) (*Group, error) {
	id := uuid.New().String()
	now := time.Now()

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err = tx.Exec(
		`INSERT INTO groups (id, name, owner_id, created_at) VALUES (?, ?, ?, ?)`,
		id, name, ownerID, now,
	); err != nil {
		return nil, err
	}

	if _, err = tx.Exec(
		`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`,
		id, ownerID,
	); err != nil {
		return nil, err
	}

	if _, err = tx.Exec(
		`INSERT INTO channels (id, group_id, name, type, created_at) VALUES (?, ?, 'geral', 'text', ?)`,
		uuid.New().String(), id, now,
	); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &Group{ID: id, Name: name, OwnerID: ownerID, CreatedAt: now}, nil
}

// ListForUser returns all groups the given user is a member of.
func ListForUser(db *sql.DB, userID string) ([]Group, error) {
	rows, err := db.Query(`
		SELECT g.id, g.name, g.owner_id, g.created_at
		FROM groups g
		JOIN group_members gm ON gm.group_id = g.id
		WHERE gm.user_id = ?
		ORDER BY g.created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// IsMember returns true when userID belongs to groupID.
func IsMember(db *sql.DB, groupID, userID string) (bool, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM group_members WHERE group_id = ? AND user_id = ?`,
		groupID, userID,
	).Scan(&count)
	return count > 0, err
}
