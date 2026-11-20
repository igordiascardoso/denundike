package auth

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/notify"
)

// AvatarMax is the largest accepted photo. The client shrinks still images to 256x256 first; only GIFs
// (which keep their animation) arrive at full size.
const AvatarMax = 3 << 20

var avatarTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true}

// AvatarMime sniffs body and reports its image type when it is an accepted photo format.
func AvatarMime(body []byte) (string, bool) {
	mime := http.DetectContentType(body)
	return mime, len(body) > 0 && avatarTypes[mime]
}

// HandleSetAvatar handles PUT /api/me/avatar with the raw image as the body -> {version}
// and tells friends and group mates through WebSocket ({"type":"avatar_changed"}).
func HandleSetAvatar(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		body, err := io.ReadAll(io.LimitReader(r.Body, AvatarMax+1))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(body) > AvatarMax {
			http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
			return
		}
		mime, okType := AvatarMime(body)
		if !okType {
			http.Error(w, "unsupported image", http.StatusBadRequest)
			return
		}
		version := time.Now().UnixMilli()
		if _, err := db.Exec(`
			INSERT INTO user_avatars (user_id, mime, data, version) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET mime = excluded.mime, data = excluded.data, version = excluded.version`,
			user.ID, mime, body, version); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		announceAvatar(db, n, user.ID, version)
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	}
}

// HandleDeleteAvatar handles DELETE /api/me/avatar: back to the colored initials.
func HandleDeleteAvatar(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if _, err := db.Exec(`DELETE FROM user_avatars WHERE user_id = ?`, user.ID); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		announceAvatar(db, n, user.ID, 0)
		writeJSON(w, http.StatusOK, map[string]any{"version": 0})
	}
}

func announceAvatar(db *sql.DB, n notify.Notifier, userID string, version int64) {
	if n == nil {
		return
	}
	n.NotifyUsers(relatedUserIDs(db, userID), map[string]any{
		"type": "avatar_changed", "user_id": userID, "version": version,
	})
}

// HandleGetAvatar handles GET /api/avatar/{id}: the image bytes (the client asks with ?v=<version>).
func HandleGetAvatar(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			mime    string
			data    []byte
			version int64
		)
		err := db.QueryRow(`SELECT mime, data, version FROM user_avatars WHERE user_id = ?`, chi.URLParam(r, "id")).
			Scan(&mime, &data, &version)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
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

// HandleAvatarList handles GET /api/avatars -> {"<user id>": version, ...} for every user that has a photo.
func HandleAvatarList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT user_id, version FROM user_avatars`)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
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
