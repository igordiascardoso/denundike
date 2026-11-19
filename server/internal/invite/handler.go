package invite

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// HandleGetInfo handles GET /api/invites/:code — public endpoint.
// Returns {created_by_login, valid, expires_at}.
func HandleGetInfo(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if code == "" {
			http.Error(w, "code required", http.StatusBadRequest)
			return
		}

		info, err := GetInfo(db, code)
		if err != nil {
			if err == ErrNotFound {
				http.Error(w, "invite not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info) //nolint:errcheck
	}
}
