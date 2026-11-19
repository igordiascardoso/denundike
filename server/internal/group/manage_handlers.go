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

const reasonGroupGone = "group_removed"

// callerRole loads the caller's role in the {id} group; it answers 403 itself
// when the caller is not a member.
func callerRole(db *sql.DB, w http.ResponseWriter, r *http.Request) (*auth.User, string, string, bool) {
	user, groupID, ok := requireMember(db, w, r)
	if !ok {
		return nil, "", "", false
	}
	role, err := RoleOf(db, groupID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, "", "", false
	}
	return user, groupID, role, true
}

// HandleRename handles PATCH /api/groups/:id {"name": "..."} (owner/admin).
func HandleRename(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		if !CanManage(role) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		name, err := Rename(db, groupID, body.Name)
		if errors.Is(err, ErrBadName) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if ids, err := MemberIDs(db, groupID); err == nil {
			n.NotifyUsers(ids, map[string]any{"type": "group_updated", "group_id": groupID, "name": name})
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": groupID, "name": name})
	}
}

// HandleDelete handles DELETE /api/groups/:id (owner only).
func HandleDelete(db *sql.DB, rt notify.Realtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		if role != RoleOwner {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name, members, channels, err := Delete(db, groupID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rt.DropChannels(db, channels, reasonGroupGone)
		rt.NotifyUsers(members, map[string]any{"type": "group_deleted", "group_id": groupID, "name": name})
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleLeave handles POST /api/groups/:id/leave.
func HandleLeave(db *sql.DB, rt notify.Realtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		if role == RoleOwner {
			http.Error(w, ErrOwnerMustTransfer.Error(), http.StatusConflict)
			return
		}
		name, _ := GroupName(db, groupID)
		channels, _ := ChannelIDs(db, groupID)
		if err := RemoveMember(db, groupID, user.ID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rt.DropUser(db, user.ID, channels, reasonGroupGone)
		rt.NotifyUsers([]string{user.ID}, map[string]any{"type": "group_removed", "group_id": groupID, "name": name, "reason": "left"})
		notifyMembers(db, rt, groupID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleKick handles DELETE /api/groups/:id/members/:uid.
// The owner may remove anyone else; an admin may remove plain members only.
func HandleKick(db *sql.DB, rt notify.Realtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		targetID := chi.URLParam(r, "uid")
		if !CanManage(role) || targetID == user.ID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		targetRole, err := RoleOf(db, groupID, targetID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if targetRole == "" {
			http.Error(w, "member not found", http.StatusNotFound)
			return
		}
		if targetRole == RoleOwner || (role == RoleAdmin && targetRole != RoleMember) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name, _ := GroupName(db, groupID)
		channels, _ := ChannelIDs(db, groupID)
		if err := RemoveMember(db, groupID, targetID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rt.DropUser(db, targetID, channels, reasonGroupGone)
		rt.NotifyUsers([]string{targetID}, map[string]any{"type": "group_removed", "group_id": groupID, "name": name, "reason": "kicked"})
		notifyMembers(db, rt, groupID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleSetRole handles PATCH /api/groups/:id/members/:uid {"role": "admin|member|owner"} (owner only).
func HandleSetRole(db *sql.DB, n notify.Notifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, groupID, role, ok := callerRole(db, w, r)
		if !ok {
			return
		}
		if role != RoleOwner {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			Role string `json:"role"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "role required", http.StatusBadRequest)
			return
		}
		err := SetRole(db, groupID, user.ID, chi.URLParam(r, "uid"), body.Role)
		switch {
		case errors.Is(err, ErrNotMember):
			http.Error(w, "member not found", http.StatusNotFound)
			return
		case errors.Is(err, ErrBadRole):
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		notifyMembers(db, n, groupID)
		w.WriteHeader(http.StatusNoContent)
	}
}
