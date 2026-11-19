package friend

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/notify"
)

// HandleRequest handles POST /api/friends/request
// Body: {"login": "<target login>"}
func HandleRequest(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			Login string `json:"login"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Login == "" {
			http.Error(w, "login required", http.StatusBadRequest)
			return
		}

		targetID, accepted, err := RequestFriend(db, user.ID, req.Login)
		if err != nil {
			switch err {
			case ErrUserNotFound:
				http.Error(w, "user not found", http.StatusNotFound)
			case ErrSelf:
				http.Error(w, "cannot add yourself", http.StatusBadRequest)
			case ErrAlreadyExists:
				http.Error(w, "friendship already exists", http.StatusConflict)
			default:
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
			return
		}

		event := map[string]any{"type": "friend_request", "login": user.Login}
		status := "pending"
		if accepted {
			event = map[string]any{"type": "friend_update", "login": user.Login}
			status = "accepted"
			n.NotifyUsers([]string{user.ID}, map[string]any{"type": "friend_update", "login": req.Login})
		}
		n.NotifyUsers([]string{targetID}, event)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": status}) //nolint:errcheck
	}
}

// HandleList handles GET /api/friends
func HandleList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		entries, err := ListFriends(db, user.ID)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if entries == nil {
			entries = []FriendEntry{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries) //nolint:errcheck
	}
}

// HandleAccept handles POST /api/friends/:id/accept
func HandleAccept(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		id := chi.URLParam(r, "id")
		requesterID, err := AcceptFriend(db, id, user.ID)
		if err != nil {
			if err == ErrFriendshipNotFound {
				http.Error(w, "friendship not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		n.NotifyUsers([]string{requesterID}, map[string]any{"type": "friend_update", "login": user.Login})
		w.WriteHeader(http.StatusOK)
	}
}

// HandleReject handles POST /api/friends/:id/reject
func HandleReject(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		id := chi.URLParam(r, "id")
		if err := RejectFriend(db, id, user.ID); err != nil {
			if err == ErrFriendshipNotFound {
				http.Error(w, "friendship not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
