package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

// Sentinel errors returned by service functions.
var (
	ErrInviteNotFound     = errors.New("invite not found or expired")
	ErrInviteUsed         = errors.New("invite already used")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionNotFound    = errors.New("session expired or not found")
)

// argon2id parameters.
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
)

// hashPassword hashes password with argon2id using a random 16-byte salt.
// Encoded format: $argon2id$v=19$m=<M>,t=<T>,p=<P>$<salt_b64>$<hash_b64>
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// verifyPassword checks password against an encoded argon2id hash.
// It reads the m/t/p parameters stored in the hash string instead of using the
// global constants, so verification stays correct even if the defaults change.
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=M,t=T,p=P", "<salt_b64>", "<hash_b64>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	// Parse stored parameters: "m=65536,t=1,p=2"
	var storedMemory uint32
	var storedTime uint32
	var storedThreads uint8
	for _, kv := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(kv, "=", 2)
		if len(pair) != 2 {
			return false
		}
		v, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return false
		}
		switch pair[0] {
		case "m":
			storedMemory = uint32(v)
		case "t":
			storedTime = uint32(v)
		case "p":
			storedThreads = uint8(v)
		}
	}
	if storedMemory == 0 || storedTime == 0 || storedThreads == 0 {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	hash := argon2.IDKey([]byte(password), salt, storedTime, storedMemory, storedThreads, uint32(len(expectedHash)))
	if len(hash) != len(expectedHash) {
		return false
	}
	var diff byte
	for i := range hash {
		diff |= hash[i] ^ expectedHash[i]
	}
	return diff == 0
}

// RegisterUser creates a new user. invite_code must be valid, unexpired, and unused.
//
// Race safety: everything runs inside a transaction.
//  1. INSERT the user (establishes the FK target for invites.used_by).
//  2. Atomically claim the invite with UPDATE … WHERE used_by IS NULL — only
//     one concurrent caller will get RowsAffected == 1; the other gets 0 and
//     the transaction is rolled back (undoing the user INSERT as well).
func RegisterUser(db *sql.DB, login, email, password, inviteCode string) (*User, error) {
	// Pre-flight: check invite exists and is not already used before hashing
	// (cheap path so we don't do argon2 work for obviously invalid codes).
	var preUsedBy sql.NullString
	var preExpires time.Time
	err := db.QueryRow(`SELECT used_by, expires_at FROM invites WHERE code = ?`, inviteCode).
		Scan(&preUsedBy, &preExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	if preUsedBy.Valid {
		return nil, ErrInviteUsed
	}
	if preExpires.Before(time.Now()) {
		return nil, ErrInviteNotFound
	}

	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck — harmless after Commit

	id := uuid.New().String()
	now := time.Now()

	// 1. Insert the user first so the FK in invites.used_by can be satisfied.
	if _, err = tx.Exec(
		`INSERT INTO users (id, login, email, password_hash) VALUES (?, ?, ?, ?)`,
		id, login, email, hash,
	); err != nil {
		return nil, err
	}

	// 2. Claim the invite atomically. The WHERE guard means only one winner.
	res, err := tx.Exec(`
		UPDATE invites
		SET used_by = ?, used_at = ?
		WHERE code = ?
		  AND used_by IS NULL
		  AND expires_at > ?`,
		id, now, inviteCode, now,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Another request won the race — rollback undoes the user INSERT too.
		tx.Rollback() //nolint:errcheck
		return nil, ErrInviteUsed
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &User{
		ID:        id,
		Login:     login,
		Email:     email,
		CreatedAt: now,
	}, nil
}

// Login verifies credentials and creates a session that expires in 30 days.
// Returns the session token and the authenticated user.
func Login(db *sql.DB, login, password string) (string, *User, error) {
	var user User
	err := db.QueryRow(
		`SELECT id, login, email, password_hash, created_at FROM users WHERE login = ?`,
		login,
	).Scan(&user.ID, &user.Login, &user.Email, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, err
	}
	if !verifyPassword(password, user.PasswordHash) {
		return "", nil, ErrInvalidCredentials
	}

	token := uuid.New().String()
	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	if _, err = db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, user.ID, expiresAt,
	); err != nil {
		return "", nil, err
	}

	return token, &user, nil
}

// Logout deletes the session associated with token.
func Logout(db *sql.DB, token string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// GetUserByToken returns the user for a valid, unexpired session token.
func GetUserByToken(db *sql.DB, token string) (*User, error) {
	var user User
	var expiresAt time.Time
	err := db.QueryRow(`
		SELECT u.id, u.login, u.email, u.password_hash, u.created_at, s.expires_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = ?
	`, token).Scan(
		&user.ID, &user.Login, &user.Email,
		&user.PasswordHash, &user.CreatedAt, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if expiresAt.Before(time.Now()) {
		return nil, ErrSessionNotFound
	}
	return &user, nil
}

// CreateInvite generates a single-use invite code that expires in 7 days.
func CreateInvite(db *sql.DB, createdBy string) (*Invite, error) {
	code := uuid.New().String()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	_, err := db.Exec(
		`INSERT INTO invites (code, created_by, expires_at) VALUES (?, ?, ?)`,
		code, createdBy, expiresAt,
	)
	if err != nil {
		return nil, err
	}
	return &Invite{
		Code:      code,
		CreatedBy: createdBy,
		ExpiresAt: expiresAt,
	}, nil
}
