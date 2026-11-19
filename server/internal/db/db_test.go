package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jemunje/denundike/internal/db"
	_ "modernc.org/sqlite"
)

// An old database (users without the newer columns) must open, keep its rows and gain the columns.
func TestMigrationKeepsDataOnOldDatabaseAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, login TEXT UNIQUE NOT NULL, email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO users (id, login, email, password_hash) VALUES ('u1', 'velho', 'velho@x.com', 'hash')`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	for i := 0; i < 2; i++ { // second open proves the ALTERs are skipped when the columns exist
		database, err := db.Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var login string
		var points int
		var recovery sql.NullString
		if err := database.QueryRow(`SELECT login, points, recovery_hash FROM users WHERE id = 'u1'`).
			Scan(&login, &points, &recovery); err != nil {
			t.Fatalf("select #%d: %v", i, err)
		}
		if login != "velho" || points != 0 || recovery.Valid {
			t.Fatalf("dados alterados: %q %d %v", login, points, recovery)
		}
		database.Close()
	}
}

// Data written to a file database survives closing and reopening (what the production volume relies on).
func TestDataSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.db")
	d1, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d1.Exec(`INSERT INTO invites (code, expires_at) VALUES ('KEEP-1', datetime('now','+1 day'))`); err != nil {
		t.Fatal(err)
	}
	d1.Close()
	d2, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	var n int
	d2.QueryRow(`SELECT COUNT(*) FROM invites WHERE code = 'KEEP-1'`).Scan(&n) //nolint:errcheck
	if n != 1 {
		t.Fatalf("convite sumiu após reabrir o banco")
	}
}

// A pre-roles database (group_members without role) keeps its rows: the group creator becomes owner, others stay members.
func TestMigrationBackfillsGroupRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, login TEXT UNIQUE NOT NULL, email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE groups (id TEXT PRIMARY KEY, name TEXT NOT NULL, owner_id TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE group_members (group_id TEXT, user_id TEXT, PRIMARY KEY(group_id, user_id))`,
		`INSERT INTO users (id, login, email, password_hash) VALUES ('u1','dono','d@x.com','h'), ('u2','membro','m@x.com','h')`,
		`INSERT INTO groups (id, name, owner_id) VALUES ('g1','Antigo','u1')`,
		`INSERT INTO group_members (group_id, user_id) VALUES ('g1','u1'), ('g1','u2')`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	for i := 0; i < 2; i++ {
		database, err := db.Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		roles := map[string]string{}
		rows, err := database.Query(`SELECT user_id, role FROM group_members WHERE group_id = 'g1'`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var u, r string
			rows.Scan(&u, &r) //nolint:errcheck
			roles[u] = r
		}
		rows.Close()
		if roles["u1"] != "owner" || roles["u2"] != "member" || len(roles) != 2 {
			t.Fatalf("roles after open #%d: %v", i, roles)
		}
		database.Close()
	}
}
