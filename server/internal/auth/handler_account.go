package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jemunje/denundike/internal/gamify"
)

const (
	recoverWindow   = 15 * time.Minute
	recoverMaxLogin = 5
	recoverMaxIP    = 20
)

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// HandleRecover handles POST /api/auth/recover {login, recovery_code, new_password}.
func HandleRecover(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Login        string `json:"login"`
			RecoveryCode string `json:"recovery_code"`
			NewPassword  string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		login := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Login), "@"))
		ipKey, loginKey := "rec-ip:"+clientIP(r), "rec-login:"+login
		if rateLimiter.blocked(ipKey, recoverMaxIP, recoverWindow) ||
			rateLimiter.blocked(loginKey, recoverMaxLogin, recoverWindow) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}

		token, user, newCode, err := RecoverPassword(db, req.Login, req.RecoveryCode, req.NewPassword)
		switch {
		case errors.Is(err, ErrWeakPassword):
			http.Error(w, "password too short", http.StatusBadRequest)
			return
		case errors.Is(err, ErrInvalidRecovery):
			rateLimiter.fail(ipKey)
			rateLimiter.fail(loginKey)
			http.Error(w, "invalid login or recovery code", http.StatusUnauthorized)
			return
		case err != nil:
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		rateLimiter.reset(loginKey)
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user, "recovery_code": newCode})
	}
}

// HandleRegenerateRecovery handles POST /api/auth/recovery-code {password} (authenticated).
func HandleRegenerateRecovery(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		key := "rec-pw:" + user.ID
		if rateLimiter.blocked(key, recoverMaxLogin, recoverWindow) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		code, err := RegenerateRecovery(db, user.ID, req.Password)
		if errors.Is(err, ErrInvalidCredentials) {
			rateLimiter.fail(key)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		rateLimiter.reset(key)
		writeJSON(w, http.StatusOK, map[string]any{"recovery_code": code})
	}
}

// HandleInitRecovery handles POST /api/auth/recovery-code/init: first code for legacy accounts.
func HandleInitRecovery(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		code, err := InitRecovery(db, user.ID)
		if errors.Is(err, ErrRecoveryExists) {
			http.Error(w, "recovery code already exists", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"recovery_code": code})
	}
}

// HandleMyInvites handles GET /api/invites/mine.
func HandleMyInvites(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		list, err := gamify.ListMine(db, user.ID)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// HandleRanking handles GET /api/ranking (top 20 plus the caller's own line).
func HandleRanking(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		all, err := gamify.Ranking(db, 0)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		top := all
		if len(top) > gamify.RankingLimit {
			top = top[:gamify.RankingLimit]
		}
		var me *gamify.RankEntry
		for i := range all {
			if all[i].UserID == user.ID {
				me = &all[i]
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"top": top, "me": me, "total": len(all)})
	}
}

// HandleMyStats handles GET /api/me/stats.
func HandleMyStats(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		st, err := gamify.GetStats(db, user.ID)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}
