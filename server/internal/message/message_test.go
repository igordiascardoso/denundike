package message_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jemunje/denundike/internal/db"
	"github.com/jemunje/denundike/internal/router"
	"github.com/jemunje/denundike/internal/ws"
)

func setupTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:msg_testdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
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
	return srv, database
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

func doGet(t *testing.T, srv *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("doGet %s: %v", path, err)
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

// createGroupAndChannel creates a group, adds a channel and returns both IDs.
func createGroupAndChannel(t *testing.T, srv *httptest.Server, token string) (groupID, channelID string) {
	t.Helper()
	gResp := doPost(t, srv, "/api/groups", map[string]string{"name": "TestGroup"}, token)
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

func TestPostMessage(t *testing.T) {
	srv, database := setupTestServer(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	token := loginUser(t, srv, database, "alice"+suffix)

	_, channelID := createGroupAndChannel(t, srv, token)

	resp := doPost(t, srv, "/api/channels/"+channelID+"/messages", map[string]string{"text": "hello"}, token)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var msg map[string]any
	json.NewDecoder(resp.Body).Decode(&msg)
	if msg["id"] == nil || msg["text"] != "hello" {
		t.Fatalf("unexpected message body: %v", msg)
	}
}

func TestListMessages_paginated(t *testing.T) {
	srv, database := setupTestServer(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	token := loginUser(t, srv, database, "bob"+suffix)

	_, channelID := createGroupAndChannel(t, srv, token)

	// Post 50 messages.
	var lastID string
	for i := 0; i < 50; i++ {
		resp := doPost(t, srv, "/api/channels/"+channelID+"/messages",
			map[string]string{"text": fmt.Sprintf("msg %d", i)}, token)
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		lastID, _ = m["id"].(string)
		resp.Body.Close()
	}

	// First page: should return 50 messages.
	listResp := doGet(t, srv, "/api/channels/"+channelID+"/messages", token)
	defer listResp.Body.Close()
	var msgs []map[string]any
	json.NewDecoder(listResp.Body).Decode(&msgs)
	if len(msgs) != 50 {
		t.Fatalf("expected 50 messages, got %d", len(msgs))
	}

	// Post one more and paginate before the last of the original 50.
	doPost(t, srv, "/api/channels/"+channelID+"/messages",
		map[string]string{"text": "extra"}, token)

	// Use before cursor: should get messages before lastID.
	cursorResp := doGet(t, srv, "/api/channels/"+channelID+"/messages?before="+lastID, token)
	defer cursorResp.Body.Close()
	var cursorMsgs []map[string]any
	json.NewDecoder(cursorResp.Body).Decode(&cursorMsgs)
	// Before the last inserted (index 49) there are 49 messages.
	if len(cursorMsgs) != 49 {
		t.Fatalf("expected 49 messages with cursor, got %d", len(cursorMsgs))
	}
}
