package group

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/notify"
)

// HandleSetAvatar handles PUT /api/groups/:id/avatar with the raw image as the body -> {version} (owner/admin).
// Members are told through WebSocket ({"type":"group_avatar_changed"}).
func HandleSetAvatar(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		if !CanManage(role) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, auth.AvatarMax+1))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(body) > auth.AvatarMax {
			http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
			return
		}
		mime, okType := auth.AvatarMime(body)
		if !okType {
			http.Error(w, "unsupported image", http.StatusBadRequest)
			return
		}
		version := time.Now().UnixMilli()
		if _, err := db.Exec(`
			INSERT INTO group_avatars (group_id, mime, data, version) VALUES (?, ?, ?, ?)
			ON CONFLICT(group_id) DO UPDATE SET mime = excluded.mime, data = excluded.data, version = excluded.version`,
			groupID, mime, body, version); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		announceGroupAvatar(db, n, groupID, version)
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	}
}

// HandleDeleteAvatar handles DELETE /api/groups/:id/avatar (owner/admin): back to the initials.
func HandleDeleteAvatar(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		if !CanManage(role) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if _, err := db.Exec(`DELETE FROM group_avatars WHERE group_id = ?`, groupID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		announceGroupAvatar(db, n, groupID, 0)
		writeJSON(w, http.StatusOK, map[string]any{"version": 0})
	}
}

func announceGroupAvatar(db *sql.DB, n notify.Notifier, groupID string, version int64) {
	if n == nil {
		return
	}
	if ids, err := MemberIDs(db, groupID); err == nil {
		n.NotifyUsers(ids, map[string]any{"type": "group_avatar_changed", "group_id": groupID, "version": version})
	}
}

// HandleGetAvatar handles GET /api/group-avatar/:id: the image bytes, for members of that group only.
func HandleGetAvatar(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		groupID := chi.URLParam(r, "id")
		if member, err := IsMember(db, groupID, user.ID); err != nil || !member {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var (
			mime    string
			data    []byte
			version int64
		)
		err := db.QueryRow(`SELECT mime, data, version FROM group_avatars WHERE group_id = ?`, groupID).Scan(&mime, &data, &version)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		etag := `"` + strconv.FormatInt(version, 10) + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(data) //nolint:errcheck
	}
}

// HandleAvatarList handles GET /api/group-avatars -> {"<group id>": version} for the caller's groups that have a photo.
func HandleAvatarList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		rows, err := db.Query(`
			SELECT a.group_id, a.version FROM group_avatars a
			JOIN group_members m ON m.group_id = a.group_id WHERE m.user_id = ?`, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := map[string]int64{}
		for rows.Next() {
			var id string
			var v int64
			if rows.Scan(&id, &v) == nil {
				out[id] = v
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
