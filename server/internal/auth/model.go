package auth

import "time"

// User represents an authenticated account.
type User struct {
	ID           string    `json:"id"`
	Login        string    `json:"login"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Session ties a bearer token to a user with an expiry.
type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Invite is a single-use registration code.
type Invite struct {
	Code      string     `json:"code"`
	CreatedBy string     `json:"created_by,omitempty"`
	UsedBy    *string    `json:"used_by,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
}
