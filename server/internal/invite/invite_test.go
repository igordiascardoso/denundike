package invite_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jemunje/denundike/internal/db"
	"github.com/jemunje/denundike/internal/router"
	"github.com/jemunje/denundike/internal/ws"
)

var seedCounter int64

func setupTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:inv_testdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
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

// seedUser inserts a user and returns its ID.
func seedUser(t *testing.T, database *sql.DB, login string) string {
	t.Helper()
	id := fmt.Sprintf("user-%d-%d", time.Now().UnixNano(), atomic.AddInt64(&seedCounter, 1))
	_, err := database.Exec(
		`INSERT INTO users (id, login, email, password_hash) VALUES (?, ?, ?, ?)`,
		id, login, login+"@example.com", "hash",
	)
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
	return id
}

// seedInvite inserts a valid invite created by the given userID.
func seedInvite(t *testing.T, database *sql.DB, createdByID string, used bool) string {
	t.Helper()
	code := fmt.Sprintf("inv-%d", time.Now().UnixNano())
	_, err := database.Exec(
		`INSERT INTO invites (code, created_by, expires_at) VALUES (?, ?, ?)`,
		code,
		sql.NullString{String: createdByID, Valid: createdByID != ""},
		time.Now().Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("seedInvite: %v", err)
	}
	if used {
		usedByID := seedUser(t, database, fmt.Sprintf("used-%d", time.Now().UnixNano()))
		_, err = database.Exec(
			`UPDATE invites SET used_by = ?, used_at = ? WHERE code = ?`,
			usedByID, time.Now(), code,
		)
		if err != nil {
			t.Fatalf("seedInvite mark used: %v", err)
		}
	}
	return code
}

func TestGetInviteInfo_valid(t *testing.T) {
	srv, database := setupTestServer(t)
	creatorID := seedUser(t, database, fmt.Sprintf("creator-%d", time.Now().UnixNano()))
	code := seedInvite(t, database, creatorID, false)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/invites/"+code, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/invites/%s: %v", code, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)

	valid, ok := body["valid"].(bool)
	if !ok || !valid {
		t.Fatalf("expected valid=true, got %v", body)
	}
}

func TestGetInviteInfo_used(t *testing.T) {
	srv, database := setupTestServer(t)
	creatorID := seedUser(t, database, fmt.Sprintf("creator2-%d", time.Now().UnixNano()))
	code := seedInvite(t, database, creatorID, true)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/invites/"+code, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/invites/%s: %v", code, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)

	valid, ok := body["valid"].(bool)
	if !ok || valid {
		t.Fatalf("expected valid=false, got %v", body)
	}
}

func TestGetInviteInfo_notFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/invites/nonexistent-code", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
