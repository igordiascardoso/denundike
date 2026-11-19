package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jemunje/denundike/internal/notify"
	"github.com/jemunje/denundike/internal/uname"
)

// LoginChangeCooldown is the minimum time between two @login changes of one account.
// Decision: 24h stops hopping between names to dodge blocks/reports or confuse friends,
// while still letting someone fix a typo the next day. The first change is free.
const LoginChangeCooldown = 24 * time.Hour

var (
	ErrLoginTaken    = errors.New("login taken")
	ErrLoginSame     = errors.New("login unchanged")
	ErrLoginCooldown = errors.New("login change cooldown")
)

// loginCooldownLeft is returned (wrapped in ErrLoginCooldown) so callers can tell how long to wait.
type loginCooldownLeft struct{ left time.Duration }

func (e loginCooldownLeft) Error() string { return ErrLoginCooldown.Error() }
func (e loginCooldownLeft) Unwrap() error { return ErrLoginCooldown }

// LoginAvailable tells whether userID could use login right now.
// A user's own login always counts as free (so they can fix case/accents of it).
func LoginAvailable(db *sql.DB, userID, login string) (valid, available bool, err error) {
	login = uname.Clean(login)
	if !uname.Valid(login) {
		return false, false, nil
	}
	var other string
	err = db.QueryRow(`SELECT id FROM users WHERE login_norm = ? AND id <> ?`, uname.Norm(login), userID).Scan(&other)
	if errors.Is(err, sql.ErrNoRows) {
		return true, true, nil
	}
	if err != nil {
		return true, false, err
	}
	return true, false, nil
}

// ChangeLogin renames the @login of userID. The old name is released immediately.
func ChangeLogin(db *sql.DB, userID, newLogin string) (oldLogin string, err error) {
	newLogin = uname.Clean(newLogin)
	if !uname.Valid(newLogin) {
		return "", ErrInvalidLogin
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck

	var changed sql.NullTime
	if err = tx.QueryRow(`SELECT login, login_changed_at FROM users WHERE id = ?`, userID).Scan(&oldLogin, &changed); err != nil {
		return "", err
	}
	if oldLogin == newLogin {
		return oldLogin, ErrLoginSame
	}
	if changed.Valid {
		if left := LoginChangeCooldown - time.Since(changed.Time); left > 0 {
			return oldLogin, loginCooldownLeft{left}
		}
	}
	var other string
	err = tx.QueryRow(`SELECT id FROM users WHERE login_norm = ? AND id <> ?`, uname.Norm(newLogin), userID).Scan(&other)
	if err == nil {
		return oldLogin, ErrLoginTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return oldLogin, err
	}
	if _, err = tx.Exec(`UPDATE users SET login = ?, login_norm = ?, login_changed_at = ? WHERE id = ?`,
		newLogin, uname.Norm(newLogin), time.Now(), userID); err != nil {
		if isUnique(err) {
			return oldLogin, ErrLoginTaken
		}
		return oldLogin, err
	}
	if err = tx.Commit(); err != nil {
		return oldLogin, err
	}
	return oldLogin, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// relatedUserIDs lists everyone who should see userID's new name: friends (and pending
// requests) and members of the same groups, plus the user's other devices.
func relatedUserIDs(db *sql.DB, userID string) []string {
	rows, err := db.Query(`
		SELECT ?1
		UNION SELECT CASE WHEN requester_id = ?1 THEN addressee_id ELSE requester_id END
		      FROM friendships WHERE requester_id = ?1 OR addressee_id = ?1
		UNION SELECT gm2.user_id FROM group_members gm1
		      JOIN group_members gm2 ON gm2.group_id = gm1.group_id
		      WHERE gm1.user_id = ?1`, userID)
	if err != nil {
		return []string{userID}
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

const availMax = 60 // checks per minute per user

// HandleLoginAvailable handles GET /api/me/login-available?login=...
// -> {valid, available, login}
func HandleLoginAvailable(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		key := "avail:" + user.ID
		if rateLimiter.blocked(key, availMax, time.Minute) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		rateLimiter.fail(key)
		login := uname.Clean(r.URL.Query().Get("login"))
		valid, avail, err := LoginAvailable(db, user.ID, login)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": valid, "available": avail, "login": login})
	}
}

// HandleChangeLogin handles PATCH /api/me/login {login} -> {user}
// and tells friends and group mates through WebSocket ({"type":"user_renamed"}).
func HandleChangeLogin(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		var req struct {
			Login string `json:"login"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		old, err := ChangeLogin(db, user.ID, req.Login)
		var cd loginCooldownLeft
		switch {
		case errors.Is(err, ErrInvalidLogin):
			http.Error(w, "invalid login", http.StatusBadRequest)
			return
		case errors.Is(err, ErrLoginSame):
			http.Error(w, "login unchanged", http.StatusBadRequest)
			return
		case errors.Is(err, ErrLoginTaken):
			http.Error(w, "login taken", http.StatusConflict)
			return
		case errors.As(err, &cd):
			secs := int(cd.left.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			http.Error(w, "login change cooldown "+strconv.Itoa(secs), http.StatusTooManyRequests)
			return
		case err != nil:
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		fresh, err := GetUserByToken(db, extractToken(r))
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if rn, ok := n.(interface{ RenameUser(userID, login string) }); ok {
			rn.RenameUser(user.ID, fresh.Login)
		}
		if n != nil {
			n.NotifyUsers(relatedUserIDs(db, user.ID), map[string]any{
				"type": "user_renamed", "user_id": user.ID, "login": fresh.Login, "old_login": old,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": fresh})
	}
}
