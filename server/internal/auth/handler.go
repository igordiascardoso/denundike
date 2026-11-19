package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// HandleRegister handles POST /api/auth/register
func HandleRegister(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Login      string `json:"login"`
			Email      string `json:"email"`
			Name       string `json:"display_name"`
			Password   string `json:"password"`
			InviteCode string `json:"invite_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.InviteCode == "" {
			http.Error(w, "invite_code required", http.StatusForbidden)
			return
		}

		user, err := RegisterUserFull(db, req.Login, req.Email, req.Name, req.Password, req.InviteCode)
		if err != nil {
			switch {
			case err == ErrInviteNotFound:
				http.Error(w, "invalid or expired invite", http.StatusForbidden)
			case err == ErrInviteUsed:
				http.Error(w, "invite already used", http.StatusConflict)
			case err == ErrInvalidLogin:
				http.Error(w, "invalid login", http.StatusBadRequest)
			case err == ErrUserExists, strings.Contains(err.Error(), "UNIQUE constraint failed"):
				http.Error(w, "user already exists", http.StatusConflict)
			default:
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"user": user})
	}
}

// HandleLogin handles POST /api/auth/login
func HandleLogin(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Login    string `json:"login"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		token, user, err := Login(db, req.Login, req.Password)
		if err != nil {
			if err == ErrInvalidCredentials {
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"token": token, "user": user})
	}
}

// HandleLogout handles POST /api/auth/logout
func HandleLogout(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		Logout(db, token) //nolint:errcheck — best-effort deletion
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleMe handles GET /api/auth/me (requires RequireAuth middleware)
func HandleMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"user": user})
	}
}

// HandleCreateInvite handles POST /api/invites (requires RequireAuth middleware)
func HandleCreateInvite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		invite, err := CreateInvite(db, user.ID)
		if errors.Is(err, ErrInviteQuota) {
			http.Error(w, "invite quota reached", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(invite)
	}
}

// extractToken reads the Bearer token from the Authorization header.
func extractToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
