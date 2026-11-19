package ws

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/channel"
	"github.com/jemunje/denundike/internal/group"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 65536
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CORS is already handled by the router middleware.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Client represents a single WebSocket connection.
type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	channelID string
	userID    string
	login     string
	db        *sql.DB

	inputWin time.Time
	inputN   int
}

// HandleWS upgrades the HTTP connection to WebSocket after authenticating the
// token passed as query parameter ?token=<token>.
func HandleWS(db *sql.DB, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "token required", http.StatusUnauthorized)
			return
		}
		user, err := auth.GetUserByToken(db, token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		channelID := r.URL.Query().Get("channel_id")
		// channel_id is optional at connect time; clients can pass it to scope
		// their subscription. For broadcast tests it must be set.

		if channelID != "" {
			groupID, err := channel.GroupIDForChannel(db, channelID)
			if err != nil {
				http.Error(w, "channel not found", http.StatusNotFound)
				return
			}
			ok, err := group.IsMember(db, groupID, user.ID)
			if err != nil || !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade error: %v", err)
			return
		}

		c := &Client{
			hub:       hub,
			conn:      conn,
			send:      make(chan []byte, 256),
			channelID: channelID,
			userID:    user.ID,
			login:     user.Login,
			db:        db,
		}
		hub.Subscribe(c)
		auth.SetLastSeen(db, user.ID)

		go c.writePump()
		go c.readPump()
	}
}

// readPump reads incoming messages from the WebSocket, keeping the connection
// alive via pong/ping and handling close.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unsubscribe(c)
		c.hub.voiceDisconnect(c)
		auth.SetLastSeen(c.db, c.userID)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait)) //nolint:errcheck
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait)) //nolint:errcheck
		return nil
	})
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.handleFrame(raw)
	}
}

// writePump writes messages from the send channel to the WebSocket.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait)) //nolint:errcheck
			if !ok {
				// Hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{}) //nolint:errcheck
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			auth.TouchLastSeen(c.db, c.userID)
			c.conn.SetWriteDeadline(time.Now().Add(writeWait)) //nolint:errcheck
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
