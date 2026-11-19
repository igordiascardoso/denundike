package ws_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jemunje/denundike/internal/db"
	"github.com/jemunje/denundike/internal/router"
	"github.com/jemunje/denundike/internal/ws"
)

func setupTestServer(t *testing.T) (*httptest.Server, *sql.DB, *ws.Hub) {
	t.Helper()
	dsn := fmt.Sprintf("file:ws_testdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	hub := ws.NewHub()
	go hub.Run()
	srv := httptest.NewServer(router.New(database, hub))
	t.Cleanup(func() {
		srv.Close()
		database.Close()
	})
	return srv, database, hub
}

func seedInvite(t *testing.T, database *sql.DB) string {
	t.Helper()
	code := fmt.Sprintf("inv-%d-%d", time.Now().UnixNano(), time.Now().Nanosecond())
	_, err := database.Exec(
		`INSERT INTO invites (code, expires_at) VALUES (?, ?)`,
		code, time.Now().Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("seedInvite: %v", err)
	}
	return code
}

func doPost(t *testing.T, srv *httptest.Server, path string, body any, token string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("doPost %s: %v", path, err)
	}
	return resp
}

func loginUser(t *testing.T, srv *httptest.Server, database *sql.DB, login string) string {
	t.Helper()
	code := seedInvite(t, database)
	doPost(t, srv, "/api/auth/register", map[string]string{
		"login": login, "email": login + "@example.com",
		"password": "s3cr3t!", "invite_code": code,
	}, "")
	resp := doPost(t, srv, "/api/auth/login", map[string]string{
		"login": login, "password": "s3cr3t!",
	}, "")
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("loginUser %s: no token", login)
	}
	return token
}

func createGroupAndChannel(t *testing.T, srv *httptest.Server, token string) (groupID, channelID string) {
	t.Helper()
	gResp := doPost(t, srv, "/api/groups", map[string]string{"name": "WSTestGroup"}, token)
	var g map[string]any
	json.NewDecoder(gResp.Body).Decode(&g)
	groupID, _ = g["id"].(string)
	if groupID == "" {
		t.Fatalf("no group id")
	}
	cResp := doPost(t, srv, "/api/groups/"+groupID+"/channels", map[string]string{"name": "general", "type": "text"}, token)
	var c map[string]any
	json.NewDecoder(cResp.Body).Decode(&c)
	channelID, _ = c["id"].(string)
	if channelID == "" {
		t.Fatalf("no channel id")
	}
	return
}

// wsURL converts an http test server URL to ws://.
func wsURL(srv *httptest.Server, path string) string {
	return "ws" + srv.URL[len("http"):] + path
}

func TestWSAuthByToken(t *testing.T) {
	srv, database, _ := setupTestServer(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	token := loginUser(t, srv, database, "wsauth"+suffix)

	_, channelID := createGroupAndChannel(t, srv, token)

	conn, resp, err := websocket.DefaultDialer.Dial(
		wsURL(srv, "/ws?token="+token+"&channel_id="+channelID), nil,
	)
	if err != nil {
		t.Fatalf("ws dial failed: %v (status %v)", err, resp)
	}
	defer conn.Close()

	// 101 Switching Protocols means upgrade succeeded.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}
}

func TestWSRejectInvalidToken(t *testing.T) {
	srv, _, _ := setupTestServer(t)

	_, resp, err := websocket.DefaultDialer.Dial(
		wsURL(srv, "/ws?token=invalid-token"), nil,
	)
	if err == nil {
		t.Fatal("expected dial failure for invalid token")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWSBroadcast(t *testing.T) {
	srv, database, _ := setupTestServer(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceToken := loginUser(t, srv, database, "wsA"+suffix)
	bobToken := loginUser(t, srv, database, "wsB"+suffix)

	// Alice creates group/channel; Bob is not a member yet — we add Bob via
	// group_members directly since there's no invite-to-group endpoint yet.
	_, channelID := createGroupAndChannel(t, srv, aliceToken)

	// Add Bob to the group via DB (no HTTP endpoint for that in Phase 1).
	var groupID string
	database.QueryRow(`SELECT group_id FROM channels WHERE id = ?`, channelID).Scan(&groupID)
	var bobID string
	database.QueryRow(`SELECT id FROM users WHERE login = ?`, "wsB"+suffix).Scan(&bobID)
	database.Exec(`INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, bobID)

	// Connect both Alice and Bob to the same channel.
	aliceConn, _, err := websocket.DefaultDialer.Dial(
		wsURL(srv, "/ws?token="+aliceToken+"&channel_id="+channelID), nil,
	)
	if err != nil {
		t.Fatalf("alice ws dial: %v", err)
	}
	defer aliceConn.Close()

	bobConn, _, err := websocket.DefaultDialer.Dial(
		wsURL(srv, "/ws?token="+bobToken+"&channel_id="+channelID), nil,
	)
	if err != nil {
		t.Fatalf("bob ws dial: %v", err)
	}
	defer bobConn.Close()

	// Give the hub time to register both clients.
	time.Sleep(50 * time.Millisecond)

	// Alice posts a message via HTTP.
	resp := doPost(t, srv, "/api/channels/"+channelID+"/messages",
		map[string]string{"text": "hello bob"}, aliceToken)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post message failed: %d", resp.StatusCode)
	}

	// Bob should receive the WS event within 1 second.
	bobConn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, data, err := bobConn.ReadMessage()
	if err != nil {
		t.Fatalf("bob did not receive WS message: %v", err)
	}

	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("invalid JSON from WS: %v", err)
	}
	if event["type"] != "message" {
		t.Fatalf("expected type=message, got %v", event["type"])
	}
	msg, _ := event["message"].(map[string]any)
	if msg == nil || msg["text"] != "hello bob" {
		t.Fatalf("unexpected message in WS event: %v", event)
	}
}
