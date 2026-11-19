package channel

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/group"
	"github.com/jemunje/denundike/internal/notify"
)

// manageCtx loads the channel {id}, checks the caller may manage its group and
// answers the HTTP error itself otherwise.
func manageCtx(db *sql.DB, w http.ResponseWriter, r *http.Request) (*Channel, bool) {
	user := auth.UserFromContext(r.Context())
	var c Channel
	err := db.QueryRow(`SELECT id, group_id, name, type, created_at FROM channels WHERE id = ?`, chi.URLParam(r, "id")).
		Scan(&c.ID, &c.GroupID, &c.Name, &c.Type, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "channel not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	role, err := group.RoleOf(db, c.GroupID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if !group.CanManage(role) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, false
	}
	return &c, true
}

func notifyGroup(db *sql.DB, n notify.Notifier, groupID string, event map[string]any) {
	if ids, err := group.MemberIDs(db, groupID); err == nil {
		n.NotifyUsers(ids, event)
	}
}

// HandleRename handles PATCH /api/channels/:id {"name": "..."} (owner/admin).
func HandleRename(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := manageCtx(db, w, r)
		if !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		name, err := group.CleanName(body.Name)
		if err != nil {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		if _, err := db.Exec(`UPDATE channels SET name = ? WHERE id = ?`, name, c.ID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		c.Name = name
		notifyGroup(db, n, c.GroupID, map[string]any{"type": "channel_updated", "group_id": c.GroupID, "channel": c})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c) //nolint:errcheck
	}
}

// HandleDelete handles DELETE /api/channels/:id (owner/admin). A group always
// keeps at least one text channel.
func HandleDelete(db *sql.DB, rt notify.Realtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := manageCtx(db, w, r)
		if !ok {
			return
		}
		if c.Type == "text" {
			var texts int
			if err := db.QueryRow(`SELECT COUNT(*) FROM channels WHERE group_id = ? AND type = 'text'`, c.GroupID).Scan(&texts); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if texts <= 1 {
				http.Error(w, "group needs at least one text channel", http.StatusConflict)
				return
			}
		}
		members, _ := group.MemberIDs(db, c.GroupID)
		tx, err := db.Begin()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback() //nolint:errcheck
		if _, err = tx.Exec(`DELETE FROM messages WHERE channel_id = ?`, c.ID); err == nil {
			_, err = tx.Exec(`DELETE FROM channels WHERE id = ?`, c.ID)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rt.DropChannels(db, []string{c.ID}, "channel_deleted")
		rt.NotifyUsers(members, map[string]any{"type": "channel_deleted", "group_id": c.GroupID, "channel_id": c.ID, "name": c.Name})
		w.WriteHeader(http.StatusNoContent)
	}
}
