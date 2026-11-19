package router

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/friend"
	"github.com/jemunje/denundike/internal/invite"
)

// allowedOrigin returns true for origins that the Electron client and local dev
// servers are permitted to use: file://, app://denundike, and http://localhost:*.
func allowedOrigin(origin string) bool {
	return origin == "file://" ||
		origin == "app://denundike" ||
		strings.HasPrefix(origin, "http://localhost:")
}

// corsMiddleware adds CORS headers for Electron (file:// / app://) and dev origins.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// New builds and returns the application chi.Mux with all routes registered.
func New(db *sql.DB) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", auth.HandleRegister(db))
		r.Post("/login", auth.HandleLogin(db))
		r.Post("/logout", auth.HandleLogout(db))
		r.With(auth.RequireAuth(db)).Get("/me", auth.HandleMe())
	})

	r.With(auth.RequireAuth(db)).Post("/api/invites", auth.HandleCreateInvite(db))
	r.Get("/api/invites/{code}", invite.HandleGetInfo(db))

	r.Route("/api/friends", func(r chi.Router) {
		r.Use(auth.RequireAuth(db))
		r.Get("/", friend.HandleList(db))
		r.Post("/request", friend.HandleRequest(db))
		r.Post("/{id}/accept", friend.HandleAccept(db))
		r.Post("/{id}/reject", friend.HandleReject(db))
	})

	return r
}
