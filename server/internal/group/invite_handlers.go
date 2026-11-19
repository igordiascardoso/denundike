package group

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/notify"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// requireMember loads the caller and verifies membership of the {id} group.
func requireMember(db *sql.DB, w http.ResponseWriter, r *http.Request) (*auth.User, string, bool) {
	user := auth.UserFromContext(r.Context())
	groupID := chi.URLParam(r, "id")
	ok, err := IsMember(db, groupID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, "", false
	}
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", false
	}
	return user, groupID, true
}

func notifyMembers(db *sql.DB, n notify.Notifier, groupID string) {
	if ids, err := MemberIDs(db, groupID); err == nil {
		n.NotifyUsers(ids, map[string]any{"type": "group_members", "group_id": groupID})
	}
}

// HandleMembers handles GET /api/groups/:id/members.
func HandleMembers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, groupID, ok := requireMember(db, w, r)
		if !ok {
			return
		}
		members, err := ListMembers(db, groupID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, members)
	}
}

// HandleCreateCode handles POST /api/groups/:id/invite-code.
func HandleCreateCode(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, groupID, ok := requireMember(db, w, r)
		if !ok {
			return
		}
		code, expires, err := CreateCodeInvite(db, groupID, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expires})
	}
}

// HandleInviteFriend handles POST /api/groups/:id/invite-friend {"login": "..."}.
func HandleInviteFriend(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, groupID, ok := requireMember(db, w, r)
		if !ok {
			return
		}
		var body struct {
			Login string `json:"login"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Login == "" {
			http.Error(w, "login required", http.StatusBadRequest)
			return
		}
		inviteeID, err := InviteFriend(db, groupID, user.ID, body.Login)
		switch {
		case errors.Is(err, ErrUserNotFound):
			http.Error(w, "user not found", http.StatusNotFound)
			return
		case errors.Is(err, ErrNotFriend):
			http.Error(w, "only friends can be invited", http.StatusForbidden)
			return
		case errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrAlreadyInvited):
			http.Error(w, err.Error(), http.StatusConflict)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		name, _ := GroupName(db, groupID)
		n.NotifyUsers([]string{inviteeID}, map[string]any{
			"type": "group_invite", "group_name": name, "inviter": user.Login,
		})
		w.WriteHeader(http.StatusCreated)
	}
}

// HandleJoin handles POST /api/groups/join {"code": "..."}.
func HandleJoin(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
			http.Error(w, "code required", http.StatusBadRequest)
			return
		}
		g, err := JoinByCode(db, body.Code, user.ID)
		if errors.Is(err, ErrInviteNotFound) {
			http.Error(w, "invalid or expired code", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		notifyMembers(db, n, g.ID)
		writeJSON(w, http.StatusOK, g)
	}
}

// HandleListInvites handles GET /api/group-invites (received, pending).
func HandleListInvites(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		list, err := ListReceived(db, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// HandleAcceptInvite handles POST /api/group-invites/:id/accept.
func HandleAcceptInvite(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		g, err := AcceptInvite(db, chi.URLParam(r, "id"), user.ID)
		if errors.Is(err, ErrInviteNotFound) {
			http.Error(w, "invite not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		notifyMembers(db, n, g.ID)
		writeJSON(w, http.StatusOK, g)
	}
}

// HandleRejectInvite handles POST /api/group-invites/:id/reject.
func HandleRejectInvite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		err := RejectInvite(db, chi.URLParam(r, "id"), user.ID)
		if errors.Is(err, ErrInviteNotFound) {
			http.Error(w, "invite not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
