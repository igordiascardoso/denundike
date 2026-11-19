package friend

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jemunje/denundike/internal/uname"
)

// Sentinel errors.
var (
	ErrUserNotFound       = errors.New("target user not found")
	ErrAlreadyExists      = errors.New("friendship already exists")
	ErrFriendshipNotFound = errors.New("friendship not found")
	ErrSelf               = errors.New("cannot add yourself")
)

// RequestFriend sends a friend request from requesterID to the user identified by targetLogin.
// It returns the target's user id and whether the request completed an existing
// opposite request (in which case the friendship is accepted immediately).
func RequestFriend(db *sql.DB, requesterID, targetLogin string) (targetID string, accepted bool, err error) {
	err = db.QueryRow(`SELECT id FROM users WHERE login_norm = ?`, uname.Norm(targetLogin)).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrUserNotFound
	}
	if err != nil {
		return "", false, err
	}
	if targetID == requesterID {
		return "", false, ErrSelf
	}

	var existingID, existingRequester string
	var status Status
	err = db.QueryRow(`
		SELECT id, requester_id, status FROM friendships
		WHERE (requester_id = ? AND addressee_id = ?) OR (requester_id = ? AND addressee_id = ?)
	`, requesterID, targetID, targetID, requesterID).Scan(&existingID, &existingRequester, &status)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.Exec(`
			INSERT INTO friendships (id, requester_id, addressee_id, status)
			VALUES (?, ?, ?, 'pending')
		`, uuid.New().String(), requesterID, targetID)
		if err != nil {
			if isUniqueConstraint(err) {
				return "", false, ErrAlreadyExists
			}
			return "", false, err
		}
		return targetID, false, nil
	case err != nil:
		return "", false, err
	case status == StatusAccepted:
		return "", false, ErrAlreadyExists
	case status == StatusPending && existingRequester == requesterID:
		return "", false, ErrAlreadyExists
	case status == StatusPending:
		// The other person already asked us: this request completes the friendship.
		_, err = db.Exec(`UPDATE friendships SET status = 'accepted' WHERE id = ?`, existingID)
		return targetID, true, err
	default: // rejected earlier: reopen as a fresh request from the current requester
		_, err = db.Exec(`
			UPDATE friendships SET requester_id = ?, addressee_id = ?, status = 'pending' WHERE id = ?
		`, requesterID, targetID, existingID)
		return targetID, false, err
	}
}

// AcceptFriend sets a friendship status to "accepted" and returns the requester's id.
// Only the addressee may accept.
func AcceptFriend(db *sql.DB, friendshipID, addresseeID string) (string, error) {
	return setStatus(db, friendshipID, addresseeID, StatusAccepted)
}

// RejectFriend sets a friendship status to "rejected".
// Only the addressee may reject.
func RejectFriend(db *sql.DB, friendshipID, addresseeID string) error {
	_, err := setStatus(db, friendshipID, addresseeID, StatusRejected)
	return err
}

func setStatus(db *sql.DB, friendshipID, addresseeID string, status Status) (string, error) {
	var requesterID string
	err := db.QueryRow(
		`SELECT requester_id FROM friendships WHERE id = ? AND addressee_id = ?`,
		friendshipID, addresseeID,
	).Scan(&requesterID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrFriendshipNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := db.Exec(`UPDATE friendships SET status = ? WHERE id = ?`, status, friendshipID); err != nil {
		return "", err
	}
	return requesterID, nil
}

// ListFriends returns all friendships involving userID (as requester or addressee).
func ListFriends(db *sql.DB, userID string) ([]FriendEntry, error) {
	rows, err := db.Query(`
		SELECT
			f.id,
			CASE WHEN f.requester_id = ? THEN f.addressee_id ELSE f.requester_id END AS other_id,
			CASE WHEN f.requester_id = ? THEN 'sent' ELSE 'received' END AS direction,
			u.login,
			f.status,
			u.last_seen_at
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
		var seen sql.NullTime
		if err := rows.Scan(&e.ID, &e.UserID, &e.Direction, &e.Login, &e.Status, &seen); err != nil {
			return nil, err
		}
		if seen.Valid {
			t := seen.Time
			e.LastSeenAt = &t
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
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
