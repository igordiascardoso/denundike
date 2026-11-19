package message

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/channel"
	"github.com/jemunje/denundike/internal/group"
	"github.com/jemunje/denundike/internal/ws"
)

// HandlePost handles POST /api/channels/:id/messages.
func HandlePost(db *sql.DB, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		channelID := chi.URLParam(r, "id")

		// Verify the user is a member of the group that owns this channel.
		groupID, err := channel.GroupIDForChannel(db, channelID)
		if err != nil {
			http.Error(w, "channel not found", http.StatusNotFound)
			return
		}
		ok, err := group.IsMember(db, groupID, user.ID)
		if err != nil || !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		var body struct {
			Text    string `json:"text"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "text required", http.StatusBadRequest)
			return
		}
		text := strings.TrimSpace(body.Text)
		if text == "" {
			text = strings.TrimSpace(body.Content)
		}
		if text == "" {
			http.Error(w, "text required", http.StatusBadRequest)
			return
		}

		msg, err := Post(db, channelID, user.ID, text)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		msg.Login = user.Login

		// Broadcast to WebSocket subscribers.
		event := map[string]any{
			"type":       "message",
			"channel_id": channelID,
			"message":    msg,
		}
		if members, err := group.MemberIDs(db, groupID); err == nil {
			hub.NotifyUsers(members, event)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(msg) //nolint:errcheck
	}
}

// HandleList handles GET /api/channels/:id/messages.
func HandleList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		channelID := chi.URLParam(r, "id")

		groupID, err := channel.GroupIDForChannel(db, channelID)
		if err != nil {
			http.Error(w, "channel not found", http.StatusNotFound)
			return
		}
		ok, err := group.IsMember(db, groupID, user.ID)
		if err != nil || !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		beforeID := r.URL.Query().Get("before")
		msgs, err := List(db, channelID, beforeID, 50)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if msgs == nil {
			msgs = []Message{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs) //nolint:errcheck
	}
}
