package router

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/channel"
	"github.com/jemunje/denundike/internal/friend"
	"github.com/jemunje/denundike/internal/group"
	"github.com/jemunje/denundike/internal/invite"
	"github.com/jemunje/denundike/internal/message"
	"github.com/jemunje/denundike/internal/ws"
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
func New(db *sql.DB, hub *ws.Hub) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", auth.HandleRegister(db))
		r.Post("/login", auth.HandleLogin(db))
		r.Post("/logout", auth.HandleLogout(db))
		r.Post("/recover", auth.HandleRecover(db))
		r.With(auth.RequireAuth(db)).Post("/recovery-code", auth.HandleRegenerateRecovery(db))
		r.With(auth.RequireAuth(db)).Post("/recovery-code/init", auth.HandleInitRecovery(db))
		r.With(auth.RequireAuth(db)).Get("/me", auth.HandleMe())
	})

	r.With(auth.RequireAuth(db)).Post("/api/invites", auth.HandleCreateInvite(db))
	r.With(auth.RequireAuth(db)).Get("/api/invites/mine", auth.HandleMyInvites(db))
	r.Get("/api/invites/{code}", invite.HandleGetInfo(db))
	r.With(auth.RequireAuth(db)).Get("/api/ranking", auth.HandleRanking(db))
	r.With(auth.RequireAuth(db)).Get("/api/me/stats", auth.HandleMyStats(db))

	r.Route("/api/friends", func(r chi.Router) {
		r.Use(auth.RequireAuth(db))
		r.Get("/", friend.HandleList(db))
		r.Post("/request", friend.HandleRequest(db, hub))
		r.Post("/{id}/accept", friend.HandleAccept(db, hub))
		r.Post("/{id}/reject", friend.HandleReject(db))
	})

	// Groups & channels
	r.Route("/api/groups", func(r chi.Router) {
		r.Use(auth.RequireAuth(db))
		r.Post("/", group.HandleCreate(db))
		r.Get("/", group.HandleList(db))
		r.Post("/join", group.HandleJoin(db, hub))
		r.Get("/{id}/members", group.HandleMembers(db))
		r.Post("/{id}/invite-code", group.HandleCreateCode(db))
		r.Post("/{id}/invite-friend", group.HandleInviteFriend(db, hub))
		r.Post("/{id}/channels", channel.HandleCreate(db))
		r.Get("/{id}/channels", channel.HandleList(db))
		r.Get("/{id}/voice", ws.HandleVoiceList(db, hub))
	})

	// Direct group invitations received by the caller
	r.Route("/api/group-invites", func(r chi.Router) {
		r.Use(auth.RequireAuth(db))
		r.Get("/", group.HandleListInvites(db))
		r.Post("/{id}/accept", group.HandleAcceptInvite(db, hub))
		r.Post("/{id}/reject", group.HandleRejectInvite(db))
	})

	// Channels (messages)
	r.Route("/api/channels", func(r chi.Router) {
		r.Use(auth.RequireAuth(db))
		r.Post("/{id}/messages", message.HandlePost(db, hub))
		r.Get("/{id}/messages", message.HandleList(db))
	})

	r.With(auth.RequireAuth(db)).Get("/api/ice", ws.HandleICE())

	// WebSocket — authenticated by ?token= query param (Bearer not available on WS handshake)
	r.Get("/ws", ws.HandleWS(db, hub))

	return r
}
