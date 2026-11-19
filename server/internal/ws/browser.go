package ws

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultBrowserURL = "https://www.google.com"
	maxURLLen         = 2048
	maxInputPerSec    = 240
)

// browserSession is the shared browser of one voice room. The host runs a real
// Chrome and streams it; everybody in the room may drive it.
type browserSession struct {
	hostID    string
	hostLogin string
	url       string
}

func validWebURL(raw string) bool {
	if raw == "" || len(raw) > maxURLLen {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func browserStateMsg(channelID string, b *browserSession) map[string]any {
	if b == nil {
		return map[string]any{"type": "browser_state", "channel_id": channelID, "active": false}
	}
	return map[string]any{
		"type": "browser_state", "channel_id": channelID, "active": true,
		"host_id": b.hostID, "host_login": b.hostLogin, "url": b.url,
	}
}

func (c *Client) browserError(msg string) {
	c.hub.NotifyUsers([]string{c.userID}, map[string]any{"type": "browser_error", "error": msg})
}

// roomMembers returns the room's member ids and its browser session when
// userID is currently in channelID's room; ok is false otherwise.
func (h *Hub) roomMembers(channelID, userID string) (ids []string, b *browserSession, ok bool) {
	h.voice.mu.Lock()
	defer h.voice.mu.Unlock()
	room := h.voice.rooms[channelID]
	if room == nil {
		return nil, nil, false
	}
	if _, in := room.members[userID]; !in {
		return nil, nil, false
	}
	for id := range room.members {
		ids = append(ids, id)
	}
	return ids, room.browser, true
}

func (h *Hub) browserStart(c *Client, in inbound) {
	var d struct {
		URL string `json:"url"`
	}
	json.Unmarshal(in.Data, &d) //nolint:errcheck
	if d.URL == "" {
		d.URL = defaultBrowserURL
	}
	if !validWebURL(d.URL) {
		c.browserError("invalid url")
		return
	}
	h.voice.mu.Lock()
	room := h.voice.rooms[in.ChannelID]
	if room == nil || room.members[c.userID] == nil {
		h.voice.mu.Unlock()
		c.browserError("not in call")
		return
	}
	if room.browser != nil {
		h.voice.mu.Unlock()
		c.browserError("session active")
		return
	}
	room.browser = &browserSession{hostID: c.userID, hostLogin: c.Login(), url: d.URL}
	msg := browserStateMsg(in.ChannelID, room.browser)
	ids := memberIDs(room)
	h.voice.mu.Unlock()
	h.NotifyUsers(ids, msg)
}

func memberIDs(room *voiceRoom) []string {
	ids := make([]string, 0, len(room.members))
	for id := range room.members {
		ids = append(ids, id)
	}
	return ids
}

func (h *Hub) browserStop(c *Client, in inbound) {
	h.voice.mu.Lock()
	room := h.voice.rooms[in.ChannelID]
	if room == nil || room.browser == nil || room.browser.hostID != c.userID {
		h.voice.mu.Unlock()
		return
	}
	room.browser = nil
	ids := memberIDs(room)
	h.voice.mu.Unlock()
	h.NotifyUsers(ids, browserStateMsg(in.ChannelID, nil))
}

// browserURL lets the host publish the page it is currently on.
func (h *Hub) browserURL(c *Client, in inbound) {
	var d struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(in.Data, &d) != nil || !validWebURL(d.URL) {
		return
	}
	h.voice.mu.Lock()
	room := h.voice.rooms[in.ChannelID]
	if room == nil || room.browser == nil || room.browser.hostID != c.userID {
		h.voice.mu.Unlock()
		return
	}
	room.browser.url = d.URL
	msg := browserStateMsg(in.ChannelID, room.browser)
	ids := memberIDs(room)
	h.voice.mu.Unlock()
	h.NotifyUsers(ids, msg)
}

type navCmd struct {
	Action string `json:"action"`
	URL    string `json:"url,omitempty"`
}

type inputCmd struct {
	T      string  `json:"t"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Button string  `json:"button,omitempty"`
	DX     float64 `json:"dx,omitempty"`
	DY     float64 `json:"dy,omitempty"`
	Key    string  `json:"key,omitempty"`
	Code   string  `json:"code,omitempty"`
	Text   string  `json:"text,omitempty"`
	Mods   int     `json:"mods,omitempty"`
}

func (c *Client) inputAllowed() bool {
	now := time.Now()
	if now.Sub(c.inputWin) >= time.Second {
		c.inputWin, c.inputN = now, 0
	}
	c.inputN++
	return c.inputN <= maxInputPerSec
}

func sanitizeInput(raw json.RawMessage) (*inputCmd, bool) {
	var m inputCmd
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	switch m.T {
	case "move", "down", "up", "wheel":
		if m.X < 0 || m.X > 1 || m.Y < 0 || m.Y > 1 {
			return nil, false
		}
		if m.T == "down" || m.T == "up" {
			if m.Button != "left" && m.Button != "middle" && m.Button != "right" {
				return nil, false
			}
		} else {
			m.Button = ""
		}
		if m.T == "wheel" {
			if m.DX < -5000 || m.DX > 5000 || m.DY < -5000 || m.DY > 5000 {
				return nil, false
			}
		} else {
			m.DX, m.DY = 0, 0
		}
		m.Key, m.Code, m.Text = "", "", ""
	case "key_down", "key_up":
		if m.Key == "" || len(m.Key) > 32 || len(m.Code) > 32 || utf8.RuneCountInString(m.Text) > 8 {
			return nil, false
		}
		m.Button, m.DX, m.DY, m.X, m.Y = "", 0, 0, 0, 0
	default:
		return nil, false
	}
	if m.Mods < 0 || m.Mods > 15 {
		return nil, false
	}
	return &m, true
}

// browserCmd relays a participant's navigation or mouse/keyboard input to the
// host. Nothing but sanitized, whitelisted events ever reaches the host.
func (h *Hub) browserCmd(c *Client, in inbound, kind string) {
	ids, b, ok := h.roomMembers(in.ChannelID, c.userID)
	if !ok || b == nil {
		return
	}
	var payload any
	switch kind {
	case "nav":
		var n navCmd
		if json.Unmarshal(in.Data, &n) != nil {
			return
		}
		switch n.Action {
		case "back", "forward", "reload":
			n.URL = ""
		case "go":
			if !validWebURL(strings.TrimSpace(n.URL)) {
				c.browserError("invalid url")
				return
			}
			n.URL = strings.TrimSpace(n.URL)
		default:
			return
		}
		payload = n
	case "input":
		if !c.inputAllowed() {
			return
		}
		m, ok := sanitizeInput(in.Data)
		if !ok {
			return
		}
		payload = m
		if m.T == "move" || m.T == "down" || m.T == "up" || m.T == "wheel" {
			var others []string
			for _, id := range ids {
				if id != c.userID {
					others = append(others, id)
				}
			}
			h.NotifyUsers(others, map[string]any{
				"type": "browser_cursor", "channel_id": in.ChannelID,
				"user_id": c.userID, "login": c.Login(), "x": m.X, "y": m.Y,
			})
		}
	default:
		return
	}
	h.NotifyUsers([]string{b.hostID}, map[string]any{
		"type": "browser_cmd", "channel_id": in.ChannelID,
		"from": c.userID, "login": c.Login(), "kind": kind, "data": payload,
	})
}
