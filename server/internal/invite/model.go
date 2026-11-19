package invite

import "time"

// Info is the public view of an invite returned by GET /api/invites/:code.
type Info struct {
	CreatedByLogin string    `json:"created_by_login"`
	Valid          bool      `json:"valid"`
	ExpiresAt      time.Time `json:"expires_at"`
}
