package notify

import "database/sql"

// Notifier delivers a JSON event to every connected client of the given users.
type Notifier interface {
	NotifyUsers(userIDs []string, event any)
}

// Realtime also controls live connections: voice rooms and channel subscriptions.
type Realtime interface {
	Notifier
	// DropChannels ends voice/browser sessions and message subscriptions of the channels.
	DropChannels(db *sql.DB, channelIDs []string, reason string)
	// DropUser removes one user from voice and message subscriptions of the channels.
	DropUser(db *sql.DB, userID string, channelIDs []string, reason string)
}
