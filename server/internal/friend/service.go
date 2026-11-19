package friend

import (
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// Sentinel errors.
var (
	ErrUserNotFound    = errors.New("target user not found")
	ErrAlreadyExists   = errors.New("friendship already exists")
	ErrFriendshipNotFound = errors.New("friendship not found")
)

// RequestFriend sends a friend request from requesterID to the user identified by targetLogin.
func RequestFriend(db *sql.DB, requesterID, targetLogin string) error {
	var targetID string
	err := db.QueryRow(`SELECT id FROM users WHERE login = ?`, targetLogin).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}

	id := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO friendships (id, requester_id, addressee_id, status)
		VALUES (?, ?, ?, 'pending')
	`, id, requesterID, targetID)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

// AcceptFriend sets a friendship status to "accepted".
// Only the addressee may accept.
func AcceptFriend(db *sql.DB, friendshipID, addresseeID string) error {
	return setStatus(db, friendshipID, addresseeID, StatusAccepted)
}

// RejectFriend sets a friendship status to "rejected".
// Only the addressee may reject.
func RejectFriend(db *sql.DB, friendshipID, addresseeID string) error {
	return setStatus(db, friendshipID, addresseeID, StatusRejected)
}

func setStatus(db *sql.DB, friendshipID, addresseeID string, status Status) error {
	res, err := db.Exec(`
		UPDATE friendships SET status = ?
		WHERE id = ? AND addressee_id = ?
	`, status, friendshipID, addresseeID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFriendshipNotFound
	}
	return nil
}

// ListFriends returns all friendships involving userID (as requester or addressee).
func ListFriends(db *sql.DB, userID string) ([]FriendEntry, error) {
	rows, err := db.Query(`
		SELECT
			f.id,
			CASE WHEN f.requester_id = ? THEN f.addressee_id ELSE f.requester_id END AS other_id,
			CASE WHEN f.requester_id = ? THEN 'sent' ELSE 'received' END AS direction,
			u.login,
			f.status
		FROM friendships f
		JOIN users u ON u.id = CASE WHEN f.requester_id = ? THEN f.addressee_id ELSE f.requester_id END
		WHERE f.requester_id = ? OR f.addressee_id = ?
		ORDER BY f.created_at DESC
	`, userID, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []FriendEntry
	for rows.Next() {
		var e FriendEntry
		var id string
		if err := rows.Scan(&id, &e.UserID, &e.Direction, &e.Login, &e.Status); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// isUniqueConstraint returns true when err is a SQLite UNIQUE constraint violation.
func isUniqueConstraint(err error) bool {
	return err != nil && (contains(err.Error(), "UNIQUE constraint failed") ||
		contains(err.Error(), "constraint failed: friendships."))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
