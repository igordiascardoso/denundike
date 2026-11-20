package group

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

// Member roles inside a group.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

const maxNameLen = 50

var (
	ErrBadName           = errors.New("invalid name")
	ErrNotMember         = errors.New("not a member")
	ErrOwnerMustTransfer = errors.New("owner must transfer ownership first")
	ErrBadRole           = errors.New("invalid role")
)

// CleanName trims name and checks its length (1..50 characters).
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", ErrBadName
	}
	return name, nil
}

// RoleOf returns the role userID has in groupID, or "" when not a member.
func RoleOf(db *sql.DB, groupID, userID string) (string, error) {
	var role string
	err := db.QueryRow(`SELECT role FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// CanManage reports whether role may rename the group and manage its channels.
func CanManage(role string) bool { return role == RoleOwner || role == RoleAdmin }

// Rename sets a new name for groupID.
func Rename(db *sql.DB, groupID, name string) (string, error) {
	name, err := CleanName(name)
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`UPDATE groups SET name = ? WHERE id = ?`, name, groupID)
	return name, err
}

// ChannelIDs lists the channel ids of groupID.
func ChannelIDs(db *sql.DB, groupID string) ([]string, error) {
	rows, err := db.Query(`SELECT id FROM channels WHERE group_id = ?`, groupID)
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

// Delete removes the group with its channels, messages, invites and memberships
// in one transaction. It returns what was removed so callers can notify.
func Delete(db *sql.DB, groupID string) (name string, members, channels []string, err error) {
	if name, err = GroupName(db, groupID); err != nil {
		return "", nil, nil, err
	}
	if members, err = MemberIDs(db, groupID); err != nil {
		return "", nil, nil, err
	}
	if channels, err = ChannelIDs(db, groupID); err != nil {
		return "", nil, nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return "", nil, nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, q := range []string{
		`DELETE FROM messages WHERE channel_id IN (SELECT id FROM channels WHERE group_id = ?1)`,
		`DELETE FROM channels WHERE group_id = ?1`,
		`DELETE FROM group_avatars WHERE group_id = ?1`,
		`DELETE FROM group_invites WHERE group_id = ?1`,
		`DELETE FROM group_members WHERE group_id = ?1`,
		`DELETE FROM groups WHERE id = ?1`,
	} {
		if _, err = tx.Exec(q, groupID); err != nil {
			return "", nil, nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", nil, nil, err
	}
	return name, members, channels, nil
}

// RemoveMember deletes a user's membership (used by both kick and leave).
func RemoveMember(db *sql.DB, groupID, userID string) error {
	res, err := db.Exec(`DELETE FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotMember
	}
	return nil
}

// SetRole changes targetID's role. role "owner" transfers ownership from
// actorID (the current owner) and demotes them to admin.
func SetRole(db *sql.DB, groupID, actorID, targetID, role string) error {
	if role != RoleOwner && role != RoleAdmin && role != RoleMember {
		return ErrBadRole
	}
	cur, err := RoleOf(db, groupID, targetID)
	if err != nil {
		return err
	}
	if cur == "" {
		return ErrNotMember
	}
	if cur == RoleOwner || targetID == actorID {
		return ErrBadRole
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if role == RoleOwner {
		if _, err = tx.Exec(`UPDATE groups SET owner_id = ? WHERE id = ?`, targetID, groupID); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE group_members SET role = 'admin' WHERE group_id = ? AND user_id = ?`, groupID, actorID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?`, role, groupID, targetID); err != nil {
		return err
	}
	return tx.Commit()
}
