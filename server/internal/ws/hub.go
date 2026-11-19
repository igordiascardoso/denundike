package ws

import "sync"

// Hub maintains the set of active WebSocket clients and broadcasts messages to
// clients subscribed to a specific channel.
type Hub struct {
	mu       sync.RWMutex
	channels map[string]map[*Client]bool // channelID → set of clients

	register   chan *Client
	unregister chan *Client
	broadcast  chan channelMessage
}

type channelMessage struct {
	channelID string
	data      []byte
}

// NewHub creates a Hub ready to be started with Run.
func NewHub() *Hub {
	return &Hub{
		channels:   make(map[string]map[*Client]bool),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		broadcast:  make(chan channelMessage, 256),
	}
}

// Run processes registration, unregistration and broadcast events.
// Call it in its own goroutine: go hub.Run().
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			if h.channels[c.channelID] == nil {
				h.channels[c.channelID] = make(map[*Client]bool)
			}
			h.channels[c.channelID][c] = true
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if set, ok := h.channels[c.channelID]; ok {
				delete(set, c)
				if len(set) == 0 {
					delete(h.channels, c.channelID)
				}
			}
			h.mu.Unlock()
			close(c.send)

		case cm := <-h.broadcast:
			h.mu.RLock()
			set := h.channels[cm.channelID]
			h.mu.RUnlock()
			for c := range set {
				select {
				case c.send <- cm.data:
				default:
					// slow client — drop message rather than block
				}
			}
		}
	}
}

// BroadcastToChannel sends data to every client subscribed to channelID.
func (h *Hub) BroadcastToChannel(channelID string, data []byte) {
	h.broadcast <- channelMessage{channelID: channelID, data: data}
}

// Subscribe registers a client to receive messages for its channelID.
func (h *Hub) Subscribe(c *Client) {
	h.register <- c
}

// Unsubscribe removes a client and closes its send channel.
func (h *Hub) Unsubscribe(c *Client) {
	h.unregister <- c
}
