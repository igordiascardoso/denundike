package ws

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/channel"
	"github.com/jemunje/denundike/internal/group"
)

const maxVoiceParticipants = 6

type voiceUser struct {
	UserID string `json:"user_id"`
	Login  string `json:"login"`
}

type voiceRoom struct {
	groupID string
	members map[string]*voiceMember // userID → member
	browser *browserSession
}

type voiceMember struct {
	user voiceUser
	conn *Client
}

type voiceState struct {
	mu    sync.Mutex
	rooms map[string]*voiceRoom // channelID → room
	where map[string]string     // userID → channelID
}

func newVoiceState() *voiceState {
	return &voiceState{rooms: map[string]*voiceRoom{}, where: map[string]string{}}
}

// inbound is the shape of every client → server voice frame.
type inbound struct {
	Type      string          `json:"type"`
	ChannelID string          `json:"channel_id"`
	To        string          `json:"to"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
}

func (c *Client) handleFrame(raw []byte) {
	var in inbound
	if err := json.Unmarshal(raw, &in); err != nil {
		return
	}
	switch in.Type {
	case "voice_join":
		c.voiceJoin(in.ChannelID)
	case "voice_leave":
		c.hub.voiceLeaveUser(c.db, c.userID, nil)
	case "signal":
		c.hub.voiceSignal(c, in)
	case "voice_media":
		c.hub.voiceMedia(c, in)
	case "browser_start":
		c.hub.browserStart(c, in)
	case "browser_stop":
		c.hub.browserStop(c, in)
	case "browser_url":
		c.hub.browserURL(c, in)
	case "browser_tabs":
		c.hub.browserTabs(c, in)
	case "browser_nav":
		c.hub.browserCmd(c, in, "nav")
	case "browser_input":
		c.hub.browserCmd(c, in, "input")
	}
}

func (c *Client) voiceError(msg string) {
	c.hub.NotifyUsers([]string{c.userID}, map[string]any{"type": "voice_error", "error": msg})
}

func (c *Client) voiceJoin(channelID string) {
	if c.db == nil || channelID == "" {
		return
	}
	var groupID, chType string
	if err := c.db.QueryRow(`SELECT group_id, type FROM channels WHERE id = ?`, channelID).Scan(&groupID, &chType); err != nil {
		c.voiceError("channel not found")
		return
	}
	if ok, err := group.IsMember(c.db, groupID, c.userID); err != nil || !ok {
		c.voiceError("forbidden")
		return
	}
	if chType != "voice" {
		c.voiceError("not a voice channel")
		return
	}

	h := c.hub
	h.voice.mu.Lock()
	if cur, ok := h.voice.where[c.userID]; ok && cur != channelID {
		h.voice.mu.Unlock()
		h.voiceLeaveUser(c.db, c.userID, nil)
		h.voice.mu.Lock()
	}
	room := h.voice.rooms[channelID]
	if room == nil {
		room = &voiceRoom{groupID: groupID, members: map[string]*voiceMember{}}
		h.voice.rooms[channelID] = room
	}
	_, rejoin := room.members[c.userID]
	if !rejoin && len(room.members) >= maxVoiceParticipants {
		h.voice.mu.Unlock()
		c.voiceError("room full")
		return
	}
	me := voiceUser{UserID: c.userID, Login: c.Login()}
	var peers []voiceUser
	var peerIDs []string
	for id, m := range room.members {
		if id == c.userID {
			continue
		}
		peers = append(peers, m.user)
		peerIDs = append(peerIDs, id)
	}
	room.members[c.userID] = &voiceMember{user: me, conn: c}
	h.voice.where[c.userID] = channelID
	var bstate map[string]any
	if room.browser != nil {
		bstate = browserStateMsg(channelID, room.browser)
	}
	h.voice.mu.Unlock()

	sort.Slice(peers, func(i, j int) bool { return peers[i].Login < peers[j].Login })
	if peers == nil {
		peers = []voiceUser{}
	}
	h.NotifyUsers([]string{c.userID}, map[string]any{"type": "voice_peers", "channel_id": channelID, "peers": peers})
	h.NotifyUsers(peerIDs, map[string]any{"type": "voice_joined", "channel_id": channelID, "user_id": c.userID, "login": c.Login()})
	if bstate != nil {
		h.NotifyUsers([]string{c.userID}, bstate)
	}
	h.voiceBroadcastState(c.db, channelID, groupID)
}

// voiceLeaveUser removes userID from whatever room it is in. If only is not
// nil, the removal happens only when that connection owns the membership.
func (h *Hub) voiceLeaveUser(db *sql.DB, userID string, only *Client) {
	h.voice.mu.Lock()
	channelID, ok := h.voice.where[userID]
	if !ok {
		h.voice.mu.Unlock()
		return
	}
	room := h.voice.rooms[channelID]
	if room == nil {
		delete(h.voice.where, userID)
		h.voice.mu.Unlock()
		return
	}
	m := room.members[userID]
	if m != nil && only != nil && m.conn != only {
		h.voice.mu.Unlock()
		return
	}
	delete(room.members, userID)
	delete(h.voice.where, userID)
	groupID := room.groupID
	var rest []string
	for id := range room.members {
		rest = append(rest, id)
	}
	var bstop map[string]any
	if room.browser != nil && room.browser.hostID == userID {
		room.browser = nil
		bstop = browserStateMsg(channelID, nil)
	}
	if len(room.members) == 0 {
		delete(h.voice.rooms, channelID)
	}
	h.voice.mu.Unlock()

	h.NotifyUsers(rest, map[string]any{"type": "voice_left", "channel_id": channelID, "user_id": userID})
	if bstop != nil {
		h.NotifyUsers(rest, bstop)
	}
	h.voiceBroadcastState(db, channelID, groupID)
}

func (h *Hub) voiceDisconnect(c *Client) {
	h.voiceLeaveUser(c.db, c.userID, c)
}

func (h *Hub) voiceUsers(channelID string) []voiceUser {
	h.voice.mu.Lock()
	defer h.voice.mu.Unlock()
	out := []voiceUser{}
	if room := h.voice.rooms[channelID]; room != nil {
		for _, m := range room.members {
			out = append(out, m.user)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Login < out[j].Login })
	return out
}

func (h *Hub) voiceBroadcastState(db *sql.DB, channelID, groupID string) {
	if db == nil {
		return
	}
	ids, err := group.MemberIDs(db, groupID)
	if err != nil {
		return
	}
	h.NotifyUsers(ids, map[string]any{
		"type": "voice_state", "group_id": groupID, "channel_id": channelID, "users": h.voiceUsers(channelID),
	})
}

// sameRoom reports whether a and b are both in channelID's room.
func (h *Hub) sameRoom(channelID, a, b string) bool {
	h.voice.mu.Lock()
	defer h.voice.mu.Unlock()
	room := h.voice.rooms[channelID]
	if room == nil {
		return false
	}
	_, okA := room.members[a]
	_, okB := room.members[b]
	return okA && okB
}

func (h *Hub) voiceSignal(c *Client, in inbound) {
	if in.To == "" || in.To == c.userID {
		return
	}
	if in.Kind != "offer" && in.Kind != "answer" && in.Kind != "ice" {
		return
	}
	if !h.sameRoom(in.ChannelID, c.userID, in.To) {
		return
	}
	h.NotifyUsers([]string{in.To}, map[string]any{
		"type": "signal", "channel_id": in.ChannelID, "from": c.userID, "kind": in.Kind, "data": in.Data,
	})
}

// voiceMedia relays a participant's mic/camera/screen state to the room.
func (h *Hub) voiceMedia(c *Client, in inbound) {
	h.voice.mu.Lock()
	room := h.voice.rooms[in.ChannelID]
	var others []string
	if room != nil {
		if _, ok := room.members[c.userID]; ok {
			for id := range room.members {
				if id != c.userID {
					others = append(others, id)
				}
			}
		}
	}
	h.voice.mu.Unlock()
	h.NotifyUsers(others, map[string]any{
		"type": "voice_media", "channel_id": in.ChannelID, "user_id": c.userID, "data": in.Data,
	})
}

// HandleVoiceList handles GET /api/groups/:id/voice: who is in each voice
// channel of the group right now.
func HandleVoiceList(db *sql.DB, hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		groupID := chi.URLParam(r, "id")
		if ok, err := group.IsMember(db, groupID, user.ID); err != nil || !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		chans, err := channel.ListForGroup(db, groupID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		type row struct {
			ChannelID string      `json:"channel_id"`
			Users     []voiceUser `json:"users"`
		}
		out := []row{}
		for _, ch := range chans {
			if ch.Type != "voice" {
				continue
			}
			if u := hub.voiceUsers(ch.ID); len(u) > 0 {
				out = append(out, row{ChannelID: ch.ID, Users: u})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out) //nolint:errcheck
	}
}

type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// ICEServers builds the list handed to clients: public STUN always, plus a
// TURN relay when TURN_URL (comma separated), TURN_USER and TURN_PASS are set.
func ICEServers() []iceServer {
	servers := []iceServer{{URLs: []string{
		"stun:stun.l.google.com:19302",
		"stun:stun1.l.google.com:19302",
		"stun:stun.cloudflare.com:3478",
	}}}
	turn := strings.TrimSpace(os.Getenv("TURN_URL"))
	user, pass := os.Getenv("TURN_USER"), os.Getenv("TURN_PASS")
	if turn != "" && user != "" && pass != "" {
		var urls []string
		for _, u := range strings.Split(turn, ",") {
			if u = strings.TrimSpace(u); u != "" {
				urls = append(urls, u)
			}
		}
		servers = append(servers, iceServer{URLs: urls, Username: user, Credential: pass})
	}
	return servers
}

// HandleICE handles GET /api/ice (authenticated).
func HandleICE() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"iceServers": ICEServers()}) //nolint:errcheck
	}
}
