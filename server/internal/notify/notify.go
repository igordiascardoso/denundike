package notify

// Notifier delivers a JSON event to every connected client of the given users.
type Notifier interface {
	NotifyUsers(userIDs []string, event any)
}
