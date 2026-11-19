package auth_test

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

// setupTestServer creates an in-memory SQLite database and a test HTTP server.
func setupTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	// Use a unique DSN per test to avoid shared-cache collisions between parallel tests.
	dsn := fmt.Sprintf("file:testdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
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

// seedInvite inserts a fresh, valid invite directly into the DB.
func seedInvite(t *testing.T, database *sql.DB, createdBy string) string {
	t.Helper()
	code := fmt.Sprintf("inv-%d", time.Now().UnixNano())
	_, err := database.Exec(
		`INSERT INTO invites (code, created_by, expires_at) VALUES (?, ?, ?)`,
		code, sql.NullString{String: createdBy, Valid: createdBy != ""}, time.Now().Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("seedInvite: %v", err)
	}
	return code
}

// doPost sends a JSON POST request, optionally with a Bearer token.
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

// doGet sends a GET request with an optional Bearer token.
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

// loginUser is a helper that registers then logs in a user, returning the token.
func loginUser(t *testing.T, srv *httptest.Server, database *sql.DB, login, password string) string {
	t.Helper()
	code := seedInvite(t, database, "")
	doPost(t, srv, "/api/auth/register", map[string]string{
		"login": login, "email": login + "@example.com",
		"password": password, "invite_code": code,
	}, "")
	resp := doPost(t, srv, "/api/auth/login", map[string]string{
		"login": login, "password": password,
	}, "")
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatal("loginUser: no token in response")
	}
	return token
}

// --- Tests ---

func TestRegisterWithValidInvite(t *testing.T) {
	srv, database := setupTestServer(t)
	code := seedInvite(t, database, "")

	resp := doPost(t, srv, "/api/auth/register", map[string]string{
		"login":       "alice",
		"email":       "alice@example.com",
		"password":    "s3cr3t!",
		"invite_code": code,
	}, "")

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatal("expected 'user' key in response body")
	}
	if user["login"] != "alice" {
		t.Fatalf("expected login=alice, got %v", user["login"])
	}
}

func TestRegisterWithoutInvite(t *testing.T) {
	srv, _ := setupTestServer(t)

	resp := doPost(t, srv, "/api/auth/register", map[string]string{
		"login":    "bob",
		"email":    "bob@example.com",
		"password": "s3cr3t!",
		// no invite_code
	}, "")

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestRegisterWithUsedInvite(t *testing.T) {
	srv, database := setupTestServer(t)
	code := seedInvite(t, database, "")

	// First registration consumes the invite.
	doPost(t, srv, "/api/auth/register", map[string]string{
		"login": "alice", "email": "alice@example.com",
		"password": "s3cr3t!", "invite_code": code,
	}, "")

	// Second registration with the same invite must fail.
	resp := doPost(t, srv, "/api/auth/register", map[string]string{
		"login": "carol", "email": "carol@example.com",
		"password": "s3cr3t!", "invite_code": code,
	}, "")

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestLoginCorrect(t *testing.T) {
	srv, database := setupTestServer(t)
	code := seedInvite(t, database, "")

	doPost(t, srv, "/api/auth/register", map[string]string{
		"login": "alice", "email": "alice@example.com",
		"password": "s3cr3t!", "invite_code": code,
	}, "")

	resp := doPost(t, srv, "/api/auth/login", map[string]string{
		"login": "alice", "password": "s3cr3t!",
	}, "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["token"] == nil {
		t.Fatal("expected token in response body")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	srv, database := setupTestServer(t)
	code := seedInvite(t, database, "")

	doPost(t, srv, "/api/auth/register", map[string]string{
		"login": "alice", "email": "alice@example.com",
		"password": "s3cr3t!", "invite_code": code,
	}, "")

	resp := doPost(t, srv, "/api/auth/login", map[string]string{
		"login": "alice", "password": "wrongpassword",
	}, "")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestGetMeValidToken(t *testing.T) {
	srv, database := setupTestServer(t)
	token := loginUser(t, srv, database, "alice", "s3cr3t!")

	resp := doGet(t, srv, "/api/auth/me", token)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	user, ok := body["user"].(map[string]any)
	if !ok || user["login"] != "alice" {
		t.Fatalf("expected user.login=alice in body, got %v", body)
	}
}

func TestGetMeExpiredToken(t *testing.T) {
	srv, database := setupTestServer(t)

	// Register a user so we have a valid user_id.
	code := seedInvite(t, database, "")
	doPost(t, srv, "/api/auth/register", map[string]string{
		"login": "alice", "email": "alice@example.com",
		"password": "s3cr3t!", "invite_code": code,
	}, "")

	var userID string
	database.QueryRow(`SELECT id FROM users WHERE login = 'alice'`).Scan(&userID)

	// Insert a session that is already expired.
	expiredToken := "expired-session-token-" + fmt.Sprintf("%d", time.Now().UnixNano())
	database.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		expiredToken, userID, time.Now().Add(-1*time.Hour),
	)

	resp := doGet(t, srv, "/api/auth/me", expiredToken)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestGetMeInvalidToken(t *testing.T) {
	srv, _ := setupTestServer(t)

	resp := doGet(t, srv, "/api/auth/me", "completely-invalid-token")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
