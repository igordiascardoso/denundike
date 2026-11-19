package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
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
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
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
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
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
func RegisterUser(db *sql.DB, login, email, password, inviteCode string) (*User, error) {
	var (
		code      string
		createdBy sql.NullString
		usedBy    sql.NullString
		expiresAt time.Time
	)
	err := db.QueryRow(
		`SELECT code, created_by, used_by, expires_at FROM invites WHERE code = ?`,
		inviteCode,
	).Scan(&code, &createdBy, &usedBy, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	if expiresAt.Before(time.Now()) {
		return nil, ErrInviteNotFound
	}
	if usedBy.Valid {
		return nil, ErrInviteUsed
	}

	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	_, err = db.Exec(
		`INSERT INTO users (id, login, email, password_hash) VALUES (?, ?, ?, ?)`,
		id, login, email, hash,
	)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if _, err = db.Exec(
		`UPDATE invites SET used_by = ?, used_at = ? WHERE code = ?`,
		id, now, inviteCode,
	); err != nil {
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
