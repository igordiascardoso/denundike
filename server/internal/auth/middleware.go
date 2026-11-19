package auth

import (
	"context"
	"database/sql"
	"net/http"
)

type contextKey string

const userContextKey contextKey = "auth_user"

// RequireAuth returns a chi-compatible middleware that validates the Bearer token
// and stores the authenticated User in the request context.
// Usage: r.Use(auth.RequireAuth(db))
func RequireAuth(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			user, err := GetUserByToken(db, token)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext extracts the authenticated User from the request context.
// Returns nil when not present (unauthenticated request).
func UserFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(userContextKey).(*User)
	return user
}
