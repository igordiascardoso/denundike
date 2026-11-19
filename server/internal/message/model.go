package message

import "time"

// Message is a text message posted to a Channel.
type Message struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"`
	UserID    string    `json:"user_id"`
	Login     string    `json:"login"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
