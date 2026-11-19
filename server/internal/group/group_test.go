package group_test

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
	dsn := fmt.Sprintf("file:grp_testdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
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

func TestCreateGroup(t *testing.T) {
	srv, database := setupTestServer(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	token := loginUser(t, srv, database, "alice"+suffix)

	resp := doPost(t, srv, "/api/groups", map[string]string{"name": "My Group"}, token)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var g map[string]any
	json.NewDecoder(resp.Body).Decode(&g)
	if g["id"] == nil || g["name"] != "My Group" {
		t.Fatalf("unexpected group body: %v", g)
	}
}

func TestListGroups_onlyMine(t *testing.T) {
	srv, database := setupTestServer(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceToken := loginUser(t, srv, database, "aliceL"+suffix)
	bobToken := loginUser(t, srv, database, "bobL"+suffix)

	// Alice creates two groups, Bob creates one.
	doPost(t, srv, "/api/groups", map[string]string{"name": "Alice Group 1"}, aliceToken)
	doPost(t, srv, "/api/groups", map[string]string{"name": "Alice Group 2"}, aliceToken)
	doPost(t, srv, "/api/groups", map[string]string{"name": "Bob Group"}, bobToken)

	resp := doGet(t, srv, "/api/groups", aliceToken)
	defer resp.Body.Close()

	var groups []map[string]any
	json.NewDecoder(resp.Body).Decode(&groups)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for Alice, got %d: %v", len(groups), groups)
	}
}
