package group_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func doReq(t *testing.T, srv *httptest.Server, method, path string, body any, token string) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func status(t *testing.T, srv *httptest.Server, method, path string, body any, token string) int {
	t.Helper()
	resp := doReq(t, srv, method, path, body, token)
	resp.Body.Close()
	return resp.StatusCode
}

func wantStatus(t *testing.T, want int, label string, srv *httptest.Server, method, path string, body any, token string) {
	t.Helper()
	if got := status(t, srv, method, path, body, token); got != want {
		t.Fatalf("%s: %s %s want %d, got %d", label, method, path, want, got)
	}
}

// newGroupWorld registers alice (owner), bob, carol, dave (members) and a group.
func newGroupWorld(t *testing.T) (*httptest.Server, map[string]string, map[string]string, string) {
	t.Helper()
	srv, database := setupTestServer(t)
	sfx := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	tok := map[string]string{}
	ids := map[string]string{}
	for _, n := range []string{"alice", "bob", "carol", "dave", "eve"} {
		tok[n] = loginUser(t, srv, database, n+sfx)
		me := decode(t, doReq(t, srv, "GET", "/api/auth/me", nil, tok[n]))
		ids[n], _ = me["id"].(string)
		if ids[n] == "" {
			if u, ok := me["user"].(map[string]any); ok {
				ids[n], _ = u["id"].(string)
			}
		}
		if ids[n] == "" {
			t.Fatalf("no id for %s: %v", n, me)
		}
	}
	g := decode(t, doReq(t, srv, "POST", "/api/groups", map[string]string{"name": "Casa"}, tok["alice"]))
	gid := g["id"].(string)
	for _, n := range []string{"bob", "carol", "dave"} {
		code := decode(t, doReq(t, srv, "POST", "/api/groups/"+gid+"/invite-code", nil, tok["alice"]))["code"].(string)
		wantStatus(t, 200, "join "+n, srv, "POST", "/api/groups/join", map[string]string{"code": code}, tok[n])
	}
	return srv, ids, tok, gid
}

func roleMap(t *testing.T, srv *httptest.Server, gid, token string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range decodeList(t, doReq(t, srv, "GET", "/api/groups/"+gid+"/members", nil, token)) {
		out[m["id"].(string)] = m["role"].(string)
	}
	return out
}

func TestGroupRolesAndPermissions(t *testing.T) {
	srv, ids, tok, gid := newGroupWorld(t)

	roles := roleMap(t, srv, gid, tok["alice"])
	if roles[ids["alice"]] != "owner" || roles[ids["bob"]] != "member" {
		t.Fatalf("initial roles: %v", roles)
	}
	list := decodeList(t, doReq(t, srv, "GET", "/api/groups/", nil, tok["alice"]))
	if len(list) != 1 || list[0]["role"] != "owner" {
		t.Fatalf("group list role for owner: %v", list)
	}

	// Plain members and outsiders can do nothing administrative.
	wantStatus(t, 403, "member renames", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": "X"}, tok["bob"])
	wantStatus(t, 403, "outsider renames", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": "X"}, tok["eve"])
	wantStatus(t, 403, "member deletes", srv, "DELETE", "/api/groups/"+gid, nil, tok["bob"])
	wantStatus(t, 403, "outsider deletes", srv, "DELETE", "/api/groups/"+gid, nil, tok["eve"])
	wantStatus(t, 403, "member kicks", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["carol"], nil, tok["bob"])
	wantStatus(t, 403, "member promotes", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["bob"], map[string]string{"role": "admin"}, tok["bob"])
	wantStatus(t, 403, "outsider leaves", srv, "POST", "/api/groups/"+gid+"/leave", nil, tok["eve"])

	// Owner renames; validation applies.
	wantStatus(t, 400, "empty name", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": "   "}, tok["alice"])
	wantStatus(t, 400, "long name", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": strings.Repeat("a", 51)}, tok["alice"])
	wantStatus(t, 200, "owner renames", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": "  Casa Nova "}, tok["alice"])
	if l := decodeList(t, doReq(t, srv, "GET", "/api/groups/", nil, tok["bob"])); l[0]["name"] != "Casa Nova" {
		t.Fatalf("rename not visible: %v", l)
	}

	// Owner cannot leave without transferring; members can.
	wantStatus(t, 409, "owner leaves", srv, "POST", "/api/groups/"+gid+"/leave", nil, tok["alice"])
	wantStatus(t, 204, "dave leaves", srv, "POST", "/api/groups/"+gid+"/leave", nil, tok["dave"])
	wantStatus(t, 403, "dave gone", srv, "GET", "/api/groups/"+gid+"/channels", nil, tok["dave"])

	// Promotion is owner-only; the owner role cannot be set directly to self or via member route.
	wantStatus(t, 204, "promote bob", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["bob"], map[string]string{"role": "admin"}, tok["alice"])
	wantStatus(t, 204, "promote carol", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["carol"], map[string]string{"role": "admin"}, tok["alice"])
	wantStatus(t, 400, "bad role", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["bob"], map[string]string{"role": "king"}, tok["alice"])
	wantStatus(t, 400, "role on self", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["alice"], map[string]string{"role": "member"}, tok["alice"])
	wantStatus(t, 404, "role on stranger", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["eve"], map[string]string{"role": "admin"}, tok["alice"])

	// Admins manage but are limited: rename yes, delete no, kick plain members only.
	wantStatus(t, 200, "admin renames", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": "Casa Admin"}, tok["bob"])
	wantStatus(t, 403, "admin deletes", srv, "DELETE", "/api/groups/"+gid, nil, tok["bob"])
	wantStatus(t, 403, "admin kicks admin", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["carol"], nil, tok["bob"])
	wantStatus(t, 403, "admin kicks owner", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["alice"], nil, tok["bob"])
	wantStatus(t, 403, "admin promotes", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["carol"], map[string]string{"role": "member"}, tok["bob"])
	wantStatus(t, 403, "owner kicks self", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["alice"], nil, tok["alice"])

	// Owner demotes carol, admin bob can now kick her.
	wantStatus(t, 204, "demote carol", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["carol"], map[string]string{"role": "member"}, tok["alice"])
	wantStatus(t, 204, "admin kicks member", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["carol"], nil, tok["bob"])
	wantStatus(t, 404, "kick again", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["carol"], nil, tok["bob"])
	wantStatus(t, 403, "carol gone", srv, "GET", "/api/groups/"+gid+"/members", nil, tok["carol"])

	// Ownership transfer: alice becomes admin, bob owner, only bob can delete.
	wantStatus(t, 204, "transfer", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["bob"], map[string]string{"role": "owner"}, tok["alice"])
	roles = roleMap(t, srv, gid, tok["bob"])
	if roles[ids["bob"]] != "owner" || roles[ids["alice"]] != "admin" {
		t.Fatalf("roles after transfer: %v", roles)
	}
	if l := decodeList(t, doReq(t, srv, "GET", "/api/groups/", nil, tok["bob"])); l[0]["owner_id"] != ids["bob"] {
		t.Fatalf("owner_id after transfer: %v", l)
	}
	wantStatus(t, 403, "ex-owner deletes", srv, "DELETE", "/api/groups/"+gid, nil, tok["alice"])
	wantStatus(t, 204, "ex-owner leaves", srv, "POST", "/api/groups/"+gid+"/leave", nil, tok["alice"])
	wantStatus(t, 204, "new owner deletes", srv, "DELETE", "/api/groups/"+gid, nil, tok["bob"])
	if l := decodeList(t, doReq(t, srv, "GET", "/api/groups/", nil, tok["bob"])); len(l) != 0 {
		t.Fatalf("group still listed after delete: %v", l)
	}
	wantStatus(t, 403, "deleted group", srv, "GET", "/api/groups/"+gid+"/channels", nil, tok["bob"])
}

func TestChannelManage(t *testing.T) {
	srv, ids, tok, gid := newGroupWorld(t)
	chans := decodeList(t, doReq(t, srv, "GET", "/api/groups/"+gid+"/channels", nil, tok["alice"]))
	var text, voice string
	for _, c := range chans {
		if c["type"] == "text" {
			text = c["id"].(string)
		} else {
			voice = c["id"].(string)
		}
	}

	wantStatus(t, 403, "member renames channel", srv, "PATCH", "/api/channels/"+text, map[string]string{"name": "x"}, tok["bob"])
	wantStatus(t, 403, "outsider renames channel", srv, "PATCH", "/api/channels/"+text, map[string]string{"name": "x"}, tok["eve"])
	wantStatus(t, 403, "member deletes channel", srv, "DELETE", "/api/channels/"+text, nil, tok["bob"])
	wantStatus(t, 404, "unknown channel", srv, "DELETE", "/api/channels/nope", nil, tok["alice"])
	wantStatus(t, 400, "empty channel name", srv, "PATCH", "/api/channels/"+text, map[string]string{"name": " "}, tok["alice"])
	wantStatus(t, 200, "owner renames channel", srv, "PATCH", "/api/channels/"+text, map[string]string{"name": "papo"}, tok["alice"])

	// The last text channel can never be deleted.
	wantStatus(t, 409, "last text channel", srv, "DELETE", "/api/channels/"+text, nil, tok["alice"])

	// A second text channel can be created by an admin and the first one removed.
	wantStatus(t, 204, "promote bob", srv, "PATCH", "/api/groups/"+gid+"/members/"+ids["bob"], map[string]string{"role": "admin"}, tok["alice"])
	nc := decode(t, doReq(t, srv, "POST", "/api/groups/"+gid+"/channels", map[string]string{"name": "extra"}, tok["bob"]))
	extra := nc["id"].(string)
	wantStatus(t, 201, "post message", srv, "POST", "/api/channels/"+text+"/messages", map[string]string{"text": "ola"}, tok["carol"])
	wantStatus(t, 204, "admin deletes channel", srv, "DELETE", "/api/channels/"+text, nil, tok["bob"])
	wantStatus(t, 404, "deleted channel messages", srv, "GET", "/api/channels/"+text+"/messages", nil, tok["carol"])
	for _, c := range decodeList(t, doReq(t, srv, "GET", "/api/groups/"+gid+"/channels", nil, tok["carol"])) {
		if c["id"] == text {
			t.Fatalf("deleted channel still listed")
		}
	}
	// Voice channels can go (even the only one); text channel "extra" is now the last one.
	wantStatus(t, 204, "delete voice", srv, "DELETE", "/api/channels/"+voice, nil, tok["alice"])
	wantStatus(t, 409, "extra is last", srv, "DELETE", "/api/channels/"+extra, nil, tok["alice"])
}

type evStream struct {
	ch chan map[string]any
}

func listen(t *testing.T, srv *httptest.Server, token, channelID string) (*evStream, *websocket.Conn) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
	if channelID != "" {
		u += "&channel_id=" + channelID
	}
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	s := &evStream{ch: make(chan map[string]any, 64)}
	go func() {
		for {
			var ev map[string]any
			if err := conn.ReadJSON(&ev); err != nil {
				return
			}
			s.ch <- ev
		}
	}()
	return s, conn
}

func (s *evStream) wait(t *testing.T, typ string) map[string]any {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-s.ch:
			if ev["type"] == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", typ)
		}
	}
}

func (s *evStream) expectNone(t *testing.T, typ string) {
	t.Helper()
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case ev := <-s.ch:
			if ev["type"] == typ {
				t.Fatalf("unexpected %q event: %v", typ, ev)
			}
		case <-deadline:
			return
		}
	}
}

func TestManageRealtimeEvents(t *testing.T) {
	srv, ids, tok, gid := newGroupWorld(t)
	chans := decodeList(t, doReq(t, srv, "GET", "/api/groups/"+gid+"/channels", nil, tok["alice"]))
	var text, voice string
	for _, c := range chans {
		if c["type"] == "text" {
			text = c["id"].(string)
		} else {
			voice = c["id"].(string)
		}
	}

	bob, _ := listen(t, srv, tok["bob"], "")
	carol, _ := listen(t, srv, tok["carol"], text) // subscribed to the text channel

	wantStatus(t, 200, "rename group", srv, "PATCH", "/api/groups/"+gid, map[string]string{"name": "Nova"}, tok["alice"])
	if ev := bob.wait(t, "group_updated"); ev["name"] != "Nova" || ev["group_id"] != gid {
		t.Fatalf("group_updated: %v", ev)
	}

	nc := decode(t, doReq(t, srv, "POST", "/api/groups/"+gid+"/channels", map[string]string{"name": "novo"}, tok["alice"]))
	ev := bob.wait(t, "channel_created")
	if c, _ := ev["channel"].(map[string]any); c["id"] != nc["id"] {
		t.Fatalf("channel_created: %v", ev)
	}
	wantStatus(t, 200, "rename channel", srv, "PATCH", "/api/channels/"+nc["id"].(string), map[string]string{"name": "renomeado"}, tok["alice"])
	if ev := bob.wait(t, "channel_updated"); ev["channel"].(map[string]any)["name"] != "renomeado" {
		t.Fatalf("channel_updated: %v", ev)
	}
	wantStatus(t, 204, "delete channel", srv, "DELETE", "/api/channels/"+nc["id"].(string), nil, tok["alice"])
	if ev := bob.wait(t, "channel_deleted"); ev["channel_id"] != nc["id"] {
		t.Fatalf("channel_deleted: %v", ev)
	}

	// Kicked users are told, others refresh the member list, and the kicked
	// user's open channel subscription stops receiving messages.
	wantStatus(t, 204, "kick carol", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["carol"], nil, tok["alice"])
	if ev := carol.wait(t, "group_removed"); ev["reason"] != "kicked" || ev["group_id"] != gid {
		t.Fatalf("group_removed: %v", ev)
	}
	if ev := bob.wait(t, "group_members"); ev["group_id"] != gid {
		t.Fatalf("group_members: %v", ev)
	}
	wantStatus(t, 201, "alice posts", srv, "POST", "/api/channels/"+text+"/messages", map[string]string{"text": "segredo"}, tok["alice"])
	carol.expectNone(t, "message")

	// Deleting the group tells every member.
	wantStatus(t, 204, "delete group", srv, "DELETE", "/api/groups/"+gid, nil, tok["alice"])
	if ev := bob.wait(t, "group_deleted"); ev["group_id"] != gid || ev["name"] != "Nova" {
		t.Fatalf("group_deleted: %v", ev)
	}
	_ = voice
}

func TestDeletingChannelDisconnectsVoice(t *testing.T) {
	srv, ids, tok, gid := newGroupWorld(t)
	var voice string
	for _, c := range decodeList(t, doReq(t, srv, "GET", "/api/groups/"+gid+"/channels", nil, tok["alice"])) {
		if c["type"] == "voice" {
			voice = c["id"].(string)
		}
	}
	bob, bconn := listen(t, srv, tok["bob"], "")
	if err := bconn.WriteJSON(map[string]any{"type": "voice_join", "channel_id": voice}); err != nil {
		t.Fatal(err)
	}
	bob.wait(t, "voice_peers")
	wantStatus(t, 204, "delete voice", srv, "DELETE", "/api/channels/"+voice, nil, tok["alice"])
	if ev := bob.wait(t, "voice_kicked"); ev["channel_id"] != voice || ev["reason"] != "channel_deleted" {
		t.Fatalf("voice_kicked: %v", ev)
	}

	// Being removed from the group while in a call also ends the call.
	var voice2 string
	nc := decode(t, doReq(t, srv, "POST", "/api/groups/"+gid+"/channels", map[string]string{"name": "sala2", "type": "voice"}, tok["alice"]))
	voice2 = nc["id"].(string)
	carol, cconn := listen(t, srv, tok["carol"], "")
	cconn.WriteJSON(map[string]any{"type": "voice_join", "channel_id": voice2}) //nolint:errcheck
	carol.wait(t, "voice_peers")
	wantStatus(t, 204, "kick carol", srv, "DELETE", "/api/groups/"+gid+"/members/"+ids["carol"], nil, tok["alice"])
	if ev := carol.wait(t, "voice_kicked"); ev["channel_id"] != voice2 {
		t.Fatalf("voice_kicked on kick: %v", ev)
	}
}
