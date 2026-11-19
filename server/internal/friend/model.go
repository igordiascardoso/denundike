package friend

import "time"

// Status represents the state of a friendship.
type Status string

const (
	StatusPending  Status = "pending"
	StatusAccepted Status = "accepted"
	StatusRejected Status = "rejected"
)

// Friendship represents a row in the friendships table.
type Friendship struct {
	ID          string    `json:"id"`
	RequesterID string    `json:"requester_id"`
	AddresseeID string    `json:"addressee_id"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// FriendEntry is the view returned in GET /api/friends.
type FriendEntry struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Login      string     `json:"login"`
	Direction  string     `json:"direction"` // "sent" | "received"
	Status     Status     `json:"status"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}
