package group

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound   = errors.New("user not found")
	ErrNotFriend      = errors.New("target is not a friend")
	ErrAlreadyMember  = errors.New("already a member")
	ErrAlreadyInvited = errors.New("already invited")
	ErrInviteNotFound = errors.New("invite not found")
)

// Member is a group member as shown in the member list.
type Member struct {
	ID         string     `json:"id"`
	Login      string     `json:"login"`
	IsOwner    bool       `json:"is_owner"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// ReceivedInvite is a pending direct invitation addressed to a user.
type ReceivedInvite struct {
	ID           string    `json:"id"`
	GroupID      string    `json:"group_id"`
	GroupName    string    `json:"group_name"`
	InviterLogin string    `json:"inviter_login"`
	CreatedAt    time.Time `json:"created_at"`
}

// MemberIDs returns the user ids of every member of groupID.
func MemberIDs(db *sql.DB, groupID string) ([]string, error) {
	rows, err := db.Query(`SELECT user_id FROM group_members WHERE group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListMembers returns the members of groupID with their logins.
func ListMembers(db *sql.DB, groupID string) ([]Member, error) {
	rows, err := db.Query(`
		SELECT u.id, u.login, (g.owner_id = u.id), u.last_seen_at
		FROM group_members gm
		JOIN users u ON u.id = gm.user_id
		JOIN groups g ON g.id = gm.group_id
		WHERE gm.group_id = ?
		ORDER BY u.login ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var m Member
		var seen sql.NullTime
		if err := rows.Scan(&m.ID, &m.Login, &m.IsOwner, &seen); err != nil {
			return nil, err
		}
		if seen.Valid {
			t := seen.Time
			m.LastSeenAt = &t
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// GroupName returns the name of groupID.
func GroupName(db *sql.DB, groupID string) (string, error) {
	var name string
	err := db.QueryRow(`SELECT name FROM groups WHERE id = ?`, groupID).Scan(&name)
	return name, err
}

func newCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// CreateCodeInvite creates a shareable, multi-use join code valid for 7 days.
func CreateCodeInvite(db *sql.DB, groupID, inviterID string) (string, time.Time, error) {
	code, err := newCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(7 * 24 * time.Hour)
	_, err = db.Exec(
		`INSERT INTO group_invites (id, group_id, inviter_id, code, status, expires_at) VALUES (?, ?, ?, ?, 'code', ?)`,
		uuid.New().String(), groupID, inviterID, code, expires,
	)
	return code, expires, err
}

// InviteFriend sends a direct invitation to an accepted friend of inviterID.
// It returns the invitee's user id.
func InviteFriend(db *sql.DB, groupID, inviterID, targetLogin string) (string, error) {
	targetLogin = strings.TrimPrefix(strings.TrimSpace(targetLogin), "@")
	var inviteeID string
	err := db.QueryRow(`SELECT id FROM users WHERE login = ?`, targetLogin).Scan(&inviteeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", err
	}

	var friends int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM friendships
		WHERE status = 'accepted'
		  AND ((requester_id = ? AND addressee_id = ?) OR (requester_id = ? AND addressee_id = ?))
	`, inviterID, inviteeID, inviteeID, inviterID).Scan(&friends); err != nil {
		return "", err
	}
	if friends == 0 {
		return "", ErrNotFriend
	}

	if ok, err := IsMember(db, groupID, inviteeID); err != nil {
		return "", err
	} else if ok {
		return "", ErrAlreadyMember
	}

	var pending int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM group_invites WHERE group_id = ? AND invitee_id = ? AND status = 'pending'`,
		groupID, inviteeID,
	).Scan(&pending); err != nil {
		return "", err
	}
	if pending > 0 {
		return "", ErrAlreadyInvited
	}

	_, err = db.Exec(
		`INSERT INTO group_invites (id, group_id, inviter_id, invitee_id, status) VALUES (?, ?, ?, ?, 'pending')`,
		uuid.New().String(), groupID, inviterID, inviteeID,
	)
	return inviteeID, err
}

// ListReceived returns the pending direct invitations addressed to userID.
func ListReceived(db *sql.DB, userID string) ([]ReceivedInvite, error) {
	rows, err := db.Query(`
		SELECT gi.id, gi.group_id, g.name, u.login, gi.created_at
		FROM group_invites gi
		JOIN groups g ON g.id = gi.group_id
		JOIN users u ON u.id = gi.inviter_id
		WHERE gi.invitee_id = ? AND gi.status = 'pending'
		ORDER BY gi.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ReceivedInvite{}
	for rows.Next() {
		var r ReceivedInvite
		if err := rows.Scan(&r.ID, &r.GroupID, &r.GroupName, &r.InviterLogin, &r.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func groupByID(tx *sql.Tx, id string) (*Group, error) {
	var g Group
	err := tx.QueryRow(`SELECT id, name, owner_id, created_at FROM groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.OwnerID, &g.CreatedAt)
	return &g, err
}

// AcceptInvite accepts a direct invitation and adds userID to the group.
func AcceptInvite(db *sql.DB, inviteID, userID string) (*Group, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var groupID string
	err = tx.QueryRow(
		`SELECT group_id FROM group_invites WHERE id = ? AND invitee_id = ? AND status = 'pending'`,
		inviteID, userID,
	).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE group_invites SET status = 'accepted' WHERE id = ?`, inviteID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		return nil, err
	}
	g, err := groupByID(tx, groupID)
	if err != nil {
		return nil, err
	}
	return g, tx.Commit()
}

// RejectInvite declines a direct invitation.
func RejectInvite(db *sql.DB, inviteID, userID string) error {
	res, err := db.Exec(
		`UPDATE group_invites SET status = 'rejected' WHERE id = ? AND invitee_id = ? AND status = 'pending'`,
		inviteID, userID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInviteNotFound
	}
	return nil
}

// JoinByCode adds userID to the group that owns a valid join code.
func JoinByCode(db *sql.DB, code, userID string) (*Group, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var groupID string
	err = tx.QueryRow(
		`SELECT group_id FROM group_invites WHERE code = ? AND expires_at > ?`, code, time.Now(),
	).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		return nil, err
	}
	g, err := groupByID(tx, groupID)
	if err != nil {
		return nil, err
	}
	return g, tx.Commit()
}
