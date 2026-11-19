package friend_test

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
)

func setupTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:fr_testdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	srv := httptest.NewServer(router.New(database))
	t.Cleanup(func() {
		srv.Close()
		database.Close()
	})
	return srv, database
}

func seedInvite(t *testing.T, database *sql.DB) string {
	t.Helper()
	code := fmt.Sprintf("inv-%d", time.Now().UnixNano())
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

// loginUser registers and logs in, returning token.
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

func TestRequestFriend_found(t *testing.T) {
	srv, database := setupTestServer(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceToken := loginUser(t, srv, database, "alice"+suffix)
	loginUser(t, srv, database, "bob"+suffix) // create bob

	resp := doPost(t, srv, "/api/friends/request", map[string]string{
		"login": "bob" + suffix,
	}, aliceToken)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestRequestFriend_notFound(t *testing.T) {
	srv, database := setupTestServer(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceToken := loginUser(t, srv, database, "alice2"+suffix)

	resp := doPost(t, srv, "/api/friends/request", map[string]string{
		"login": "nobody" + suffix,
	}, aliceToken)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAcceptFriend(t *testing.T) {
	srv, database := setupTestServer(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceToken := loginUser(t, srv, database, "alice3"+suffix)
	bobToken := loginUser(t, srv, database, "bob3"+suffix)

	// Alice sends a request to Bob.
	doPost(t, srv, "/api/friends/request", map[string]string{
		"login": "bob3" + suffix,
	}, aliceToken)

	// Bob lists friends to get the friendship ID from JSON.
	listResp := doGet(t, srv, "/api/friends", bobToken)
	var entries []map[string]any
	json.NewDecoder(listResp.Body).Decode(&entries)
	if len(entries) == 0 {
		t.Fatal("Bob has no pending friends")
	}
	friendshipID, _ := entries[0]["id"].(string)
	if friendshipID == "" {
		t.Fatal("no friendship id in response")
	}

	// Bob accepts.
	resp := doPost(t, srv, "/api/friends/"+friendshipID+"/accept", nil, bobToken)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify status is now accepted.
	listResp2 := doGet(t, srv, "/api/friends", bobToken)
	var entries2 []map[string]any
	json.NewDecoder(listResp2.Body).Decode(&entries2)
	if len(entries2) == 0 {
		t.Fatal("no entries after accept")
	}
	if entries2[0]["status"] != "accepted" {
		t.Fatalf("expected status=accepted, got %v", entries2[0]["status"])
	}
}

func TestListFriends(t *testing.T) {
	srv, database := setupTestServer(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceToken := loginUser(t, srv, database, "alice4"+suffix)
	bobToken := loginUser(t, srv, database, "bob4"+suffix)
	loginUser(t, srv, database, "carol4"+suffix)

	// Alice → Bob (will be accepted), Alice → Carol (stays pending).
	doPost(t, srv, "/api/friends/request", map[string]string{"login": "bob4" + suffix}, aliceToken)
	doPost(t, srv, "/api/friends/request", map[string]string{"login": "carol4" + suffix}, aliceToken)

	// Bob lists to get the friendship ID from JSON, then accepts.
	bobList := doGet(t, srv, "/api/friends", bobToken)
	var bobEntries []map[string]any
	json.NewDecoder(bobList.Body).Decode(&bobEntries)
	if len(bobEntries) == 0 {
		t.Fatal("Bob has no pending entries")
	}
	friendshipID, _ := bobEntries[0]["id"].(string)
	if friendshipID == "" {
		t.Fatal("no friendship id in bob's list")
	}
	doPost(t, srv, "/api/friends/"+friendshipID+"/accept", nil, bobToken)

	// Alice should see 2 entries.
	listResp := doGet(t, srv, "/api/friends", aliceToken)
	var entries []map[string]any
	json.NewDecoder(listResp.Body).Decode(&entries)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}

	statuses := map[string]bool{}
	for _, e := range entries {
		statuses[e["status"].(string)] = true
	}
	if !statuses["accepted"] || !statuses["pending"] {
		t.Fatalf("expected both accepted and pending, got statuses: %v", statuses)
	}
}
