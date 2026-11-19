package invite

import (
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when an invite code does not exist.
var ErrNotFound = errors.New("invite not found")

// GetInfo returns the public Info for a given invite code.
// The Valid field is true when the invite exists, is unexpired, and has not been used.
func GetInfo(db *sql.DB, code string) (*Info, error) {
	var createdByID sql.NullString
	var createdByLogin sql.NullString
	var usedBy sql.NullString
	var expiresAt time.Time

	err := db.QueryRow(`
		SELECT i.created_by, u.login, i.used_by, i.expires_at
		FROM invites i
		LEFT JOIN users u ON u.id = i.created_by
		WHERE i.code = ?
	`, code).Scan(&createdByID, &createdByLogin, &usedBy, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	valid := !usedBy.Valid && expiresAt.After(time.Now())

	login := ""
	if createdByLogin.Valid {
		login = createdByLogin.String
	}

	return &Info{
		CreatedByLogin: login,
		Valid:          valid,
		ExpiresAt:      expiresAt,
	}, nil
}
