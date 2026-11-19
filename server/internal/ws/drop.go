package ws

import "database/sql"

// DropChannels ends the voice rooms (and their shared browser) of channelIDs
// and stops delivering their messages to anyone. It is safe on a nil Hub.
func (h *Hub) DropChannels(_ *sql.DB, channelIDs []string, reason string) {
	if h == nil {
		return
	}
	type gone struct {
		channelID string
		ids       []string
		browser   bool
	}
	var gones []gone
	h.voice.mu.Lock()
	for _, id := range channelIDs {
		room := h.voice.rooms[id]
		if room == nil {
			continue
		}
		ids := memberIDs(room)
		for _, uid := range ids {
			delete(h.voice.where, uid)
		}
		delete(h.voice.rooms, id)
		gones = append(gones, gone{id, ids, room.browser != nil})
	}
	h.voice.mu.Unlock()
	for _, g := range gones {
		h.NotifyUsers(g.ids, map[string]any{"type": "voice_kicked", "channel_id": g.channelID, "reason": reason})
		if g.browser {
			h.NotifyUsers(g.ids, browserStateMsg(g.channelID, nil))
		}
	}
	h.mu.Lock()
	for _, id := range channelIDs {
		delete(h.channels, id)
	}
	h.mu.Unlock()
}

// DropUser removes userID from the voice room and message subscriptions of
// channelIDs (used when someone leaves a group or is removed from it).
func (h *Hub) DropUser(db *sql.DB, userID string, channelIDs []string, reason string) {
	if h == nil {
		return
	}
	set := make(map[string]bool, len(channelIDs))
	for _, id := range channelIDs {
		set[id] = true
	}
	h.voice.mu.Lock()
	cur, inRoom := h.voice.where[userID]
	h.voice.mu.Unlock()
	if inRoom && set[cur] {
		h.voiceLeaveUser(db, userID, nil)
		h.NotifyUsers([]string{userID}, map[string]any{"type": "voice_kicked", "channel_id": cur, "reason": reason})
	}
	h.mu.Lock()
	for c := range h.users[userID] {
		if !set[c.channelID] {
			continue
		}
		if subs := h.channels[c.channelID]; subs != nil {
			delete(subs, c)
			if len(subs) == 0 {
				delete(h.channels, c.channelID)
			}
		}
	}
	h.mu.Unlock()
}
