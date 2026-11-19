package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidRecovery = errors.New("invalid login or recovery code")
	ErrWeakPassword    = errors.New("password too short")
	ErrRecoveryExists  = errors.New("recovery code already exists")
	ErrTooManyAttempts = errors.New("too many attempts")
)

// codeAlphabet has 32 symbols and no 0/O/1/I, so a byte&31 draw is uniform and readable.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomCode(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)&31]
	}
	return string(out), nil
}

func groupCode(raw string, size int) string {
	var parts []string
	for i := 0; i < len(raw); i += size {
		parts = append(parts, raw[i:i+size])
	}
	return strings.Join(parts, "-")
}

// newRecovery returns a readable code (XXXX-XXXX-XXXX-XXXX) and the hash that is stored.
func newRecovery() (code, hash string, err error) {
	raw, err := randomCode(16)
	if err != nil {
		return "", "", err
	}
	return groupCode(raw, 4), hashRecovery(raw), nil
}

func hashRecovery(code string) string {
	norm := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte("denundike-recovery:" + norm))
	return hex.EncodeToString(sum[:])
}

func newInviteCode() (string, error) {
	raw, err := randomCode(10)
	if err != nil {
		return "", err
	}
	return groupCode(raw, 5), nil
}

// RecoverPassword resets the password using the recovery code, revokes every old session,
// opens a fresh one and rotates the recovery code (the new one is returned once).
func RecoverPassword(db *sql.DB, login, code, newPassword string) (token string, user *User, newCode string, err error) {
	login = strings.TrimPrefix(strings.TrimSpace(login), "@")
	if len(newPassword) < 8 {
		return "", nil, "", ErrWeakPassword
	}
	var id string
	var stored sql.NullString
	qerr := db.QueryRow(`SELECT id, recovery_hash FROM users WHERE login = ? COLLATE NOCASE`, login).Scan(&id, &stored)
	if errors.Is(qerr, sql.ErrNoRows) || (qerr == nil && !stored.Valid) || (qerr == nil && stored.String == "") {
		return "", nil, "", ErrInvalidRecovery
	}
	if qerr != nil {
		return "", nil, "", qerr
	}
	if subtle.ConstantTimeCompare([]byte(stored.String), []byte(hashRecovery(code))) != 1 {
		return "", nil, "", ErrInvalidRecovery
	}

	pwHash, err := hashPassword(newPassword)
	if err != nil {
		return "", nil, "", err
	}
	newCode, newHash, err := newRecovery()
	if err != nil {
		return "", nil, "", err
	}

	tx, err := db.Begin()
	if err != nil {
		return "", nil, "", err
	}
	defer tx.Rollback() //nolint:errcheck
	// recovery_hash in WHERE makes the code single-use even under concurrent attempts.
	res, err := tx.Exec(`UPDATE users SET password_hash = ?, recovery_hash = ? WHERE id = ? AND recovery_hash = ?`,
		pwHash, newHash, id, stored.String)
	if err != nil {
		return "", nil, "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", nil, "", ErrInvalidRecovery
	}
	if _, err = tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return "", nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return "", nil, "", err
	}

	token, err = newSession(db, id)
	if err != nil {
		return "", nil, "", err
	}
	user, err = scanUser(db.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.id = ?`, id))
	if err != nil {
		return "", nil, "", err
	}
	SetLastSeen(db, id)
	return token, user, newCode, nil
}

// RegenerateRecovery issues a new code after confirming the current password.
func RegenerateRecovery(db *sql.DB, userID, password string) (string, error) {
	var pwHash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&pwHash); err != nil {
		return "", err
	}
	if !verifyPassword(password, pwHash) {
		return "", ErrInvalidCredentials
	}
	code, hash, err := newRecovery()
	if err != nil {
		return "", err
	}
	if _, err := db.Exec(`UPDATE users SET recovery_hash = ? WHERE id = ?`, hash, userID); err != nil {
		return "", err
	}
	return code, nil
}

// InitRecovery issues the first code for an account created before recovery existed.
func InitRecovery(db *sql.DB, userID string) (string, error) {
	code, hash, err := newRecovery()
	if err != nil {
		return "", err
	}
	res, err := db.Exec(`UPDATE users SET recovery_hash = ? WHERE id = ? AND (recovery_hash IS NULL OR recovery_hash = '')`,
		hash, userID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrRecoveryExists
	}
	return code, nil
}

// limiter is a small in-memory sliding-window failure counter.
type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

var rateLimiter = &limiter{fails: map[string][]time.Time{}}

// ResetRateLimits clears all counters (tests).
func ResetRateLimits() {
	rateLimiter.mu.Lock()
	rateLimiter.fails = map[string][]time.Time{}
	rateLimiter.mu.Unlock()
}

func (l *limiter) prune(key string, window time.Duration) []time.Time {
	cut := time.Now().Add(-window)
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

func (l *limiter) blocked(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, window)) >= max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	l.fails[key] = append(l.fails[key], time.Now())
	l.mu.Unlock()
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
