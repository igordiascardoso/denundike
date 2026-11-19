package group

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/jemunje/denundike/internal/auth"
)

// HandleCreate handles POST /api/groups.
func HandleCreate(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		g, err := Create(db, user.ID, body.Name)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(g) //nolint:errcheck
	}
}

// HandleList handles GET /api/groups.
func HandleList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		groups, err := ListForUser(db, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if groups == nil {
			groups = []Group{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(groups) //nolint:errcheck
	}
}
