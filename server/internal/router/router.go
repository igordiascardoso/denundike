package router

import (
	"database/sql"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jemunje/denundike/internal/auth"
)

// New builds and returns the application chi.Mux with all routes registered.
func New(db *sql.DB) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", auth.HandleRegister(db))
		r.Post("/login", auth.HandleLogin(db))
		r.Post("/logout", auth.HandleLogout(db))
		r.With(auth.RequireAuth(db)).Get("/me", auth.HandleMe())
	})

	r.With(auth.RequireAuth(db)).Post("/api/invites", auth.HandleCreateInvite(db))

	return r
}
