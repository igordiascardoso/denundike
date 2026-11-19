package channel

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/group"
)

// HandleCreate handles POST /api/groups/:id/channels.
func HandleCreate(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		groupID := chi.URLParam(r, "id")

		ok, err := group.IsMember(db, groupID, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		var body struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		if body.Type != "" && body.Type != "text" && body.Type != "voice" {
			http.Error(w, "invalid channel type", http.StatusBadRequest)
			return
		}

		c, err := Create(db, groupID, body.Name, body.Type)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(c) //nolint:errcheck
	}
}

// HandleList handles GET /api/groups/:id/channels.
func HandleList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		groupID := chi.URLParam(r, "id")

		ok, err := group.IsMember(db, groupID, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		channels, err := ListForGroup(db, groupID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if channels == nil {
			channels = []Channel{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(channels) //nolint:errcheck
	}
}
