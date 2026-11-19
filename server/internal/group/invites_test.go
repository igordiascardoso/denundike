package group_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return m
}

func decodeList(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var l []map[string]any
	json.NewDecoder(resp.Body).Decode(&l)
	return l
}

func TestSocialFlow(t *testing.T) {
	srv, database := setupTestServer(t)
	sfx := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	alice, bob, carol := "alice"+sfx, "bob"+sfx, "carol"+sfx
	aTok := loginUser(t, srv, database, alice)
	bTok := loginUser(t, srv, database, bob)
	cTok := loginUser(t, srv, database, carol)

	// Bob listens on WebSocket (user-wide, no channel) for notifications.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + bTok
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()
	events := make(chan map[string]any, 8)
	go func() {
		for {
			var ev map[string]any
			if err := conn.ReadJSON(&ev); err != nil {
				return
			}
			events <- ev
		}
	}()
	waitEvent := func(typ string) map[string]any {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case ev := <-events:
				if ev["type"] == typ {
					return ev
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q event", typ)
			}
		}
	}

	// Alice cannot add herself.
	if r := doPost(t, srv, "/api/friends/request", map[string]string{"login": alice}, aTok); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("self request: want 400, got %d", r.StatusCode)
	}

	// Alice asks Bob; Bob is notified in real time and sees it as received.
	if r := doPost(t, srv, "/api/friends/request", map[string]string{"login": "@" + bob}, aTok); r.StatusCode != http.StatusCreated {
		t.Fatalf("friend request: want 201, got %d", r.StatusCode)
	}
	if ev := waitEvent("friend_request"); ev["login"] != alice {
		t.Fatalf("friend_request event login = %v", ev["login"])
	}
	list := decodeList(t, doGet(t, srv, "/api/friends/", bTok))
	if len(list) != 1 || list[0]["direction"] != "received" || list[0]["status"] != "pending" {
		t.Fatalf("bob friends list unexpected: %v", list)
	}
	// Repeating the request (either direction is a duplicate while pending from alice).
	if r := doPost(t, srv, "/api/friends/request", map[string]string{"login": bob}, aTok); r.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate request: want 409, got %d", r.StatusCode)
	}

	// Carol is not Alice's friend, so she cannot be invited to a group.
	g := decode(t, doPost(t, srv, "/api/groups", map[string]string{"name": "Cine"}, aTok))
	gid := g["id"].(string)
	if r := doPost(t, srv, "/api/groups/"+gid+"/invite-friend", map[string]string{"login": carol}, aTok); r.StatusCode != http.StatusForbidden {
		t.Fatalf("invite non-friend: want 403, got %d", r.StatusCode)
	}

	// New group comes with a text channel and a voice room.
	chans := decodeList(t, doGet(t, srv, "/api/groups/"+gid+"/channels", aTok))
	if len(chans) != 2 || chans[0]["name"] != "geral" || chans[1]["type"] != "voice" {
		t.Fatalf("default channel missing: %v", chans)
	}

	// Bob accepts; Alice can now invite him directly.
	if r := doPost(t, srv, "/api/friends/"+list[0]["id"].(string)+"/accept", nil, bTok); r.StatusCode != http.StatusOK {
		t.Fatalf("accept: want 200, got %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/groups/"+gid+"/invite-friend", map[string]string{"login": bob}, aTok); r.StatusCode != http.StatusCreated {
		t.Fatalf("invite friend: want 201, got %d", r.StatusCode)
	}
	if ev := waitEvent("group_invite"); ev["group_name"] != "Cine" || ev["inviter"] != alice {
		t.Fatalf("group_invite event: %v", ev)
	}
	if r := doPost(t, srv, "/api/groups/"+gid+"/invite-friend", map[string]string{"login": bob}, aTok); r.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate group invite: want 409, got %d", r.StatusCode)
	}

	inv := decodeList(t, doGet(t, srv, "/api/group-invites/", bTok))
	if len(inv) != 1 || inv[0]["group_name"] != "Cine" || inv[0]["inviter_login"] != alice {
		t.Fatalf("received invites unexpected: %v", inv)
	}
	// Someone else cannot accept Bob's invitation.
	if r := doPost(t, srv, "/api/group-invites/"+inv[0]["id"].(string)+"/accept", nil, cTok); r.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign accept: want 404, got %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/group-invites/"+inv[0]["id"].(string)+"/accept", nil, bTok); r.StatusCode != http.StatusOK {
		t.Fatalf("accept invite: want 200, got %d", r.StatusCode)
	}
	if left := decodeList(t, doGet(t, srv, "/api/group-invites/", bTok)); len(left) != 0 {
		t.Fatalf("invite should be gone after accept: %v", left)
	}

	// A message from Alice reaches Bob over WebSocket with the author's login.
	ch := chans[0]["id"].(string)
	doPost(t, srv, "/api/channels/"+ch+"/messages", map[string]string{"content": "oi bob"}, aTok).Body.Close()
	ev := waitEvent("message")
	msg, _ := ev["message"].(map[string]any)
	if msg["text"] != "oi bob" || msg["login"] != alice {
		t.Fatalf("message event: %v", ev)
	}

	// Members list shows both.
	if members := decodeList(t, doGet(t, srv, "/api/groups/"+gid+"/members", aTok)); len(members) != 2 {
		t.Fatalf("members: want 2, got %v", members)
	}

	// Carol joins with a shareable code; bad codes fail.
	code := decode(t, doPost(t, srv, "/api/groups/"+gid+"/invite-code", nil, aTok))["code"].(string)
	if r := doPost(t, srv, "/api/groups/join", map[string]string{"code": "NOPE"}, cTok); r.StatusCode != http.StatusNotFound {
		t.Fatalf("bad code: want 404, got %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/groups/join", map[string]string{"code": strings.ToLower(code)}, cTok); r.StatusCode != http.StatusOK {
		t.Fatalf("join by code: want 200, got %d", r.StatusCode)
	}
	if members := decodeList(t, doGet(t, srv, "/api/groups/"+gid+"/members", cTok)); len(members) != 3 {
		t.Fatalf("members after join: want 3, got %v", members)
	}
}
