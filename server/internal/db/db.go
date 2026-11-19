package db

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the SQLite database at path and runs all migrations.
// Tasks 2 and 3 will add their tables here when they arrive.
func Open(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// For in-memory databases, a single connection avoids "database is locked".
	database.SetMaxOpenConns(1)

	if _, err := database.Exec("PRAGMA foreign_keys = ON"); err != nil {
		database.Close()
		return nil, err
	}

	if err := migrate(database); err != nil {
		database.Close()
		return nil, err
	}

	return database, nil
}

func migrate(database *sql.DB) error {
	stmts := []string{
		// --- auth (Task 1) ---
		`CREATE TABLE IF NOT EXISTS users (
			id           TEXT PRIMARY KEY,
			login        TEXT UNIQUE NOT NULL,
			email        TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(id),
			expires_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS invites (
			code       TEXT PRIMARY KEY,
			created_by TEXT REFERENCES users(id),
			used_by    TEXT REFERENCES users(id),
			used_at    DATETIME,
			expires_at DATETIME NOT NULL
		)`,
		// --- friends (Task 2) ---
		`CREATE TABLE IF NOT EXISTS friendships (
			id           TEXT PRIMARY KEY,
			requester_id TEXT NOT NULL REFERENCES users(id),
			addressee_id TEXT NOT NULL REFERENCES users(id),
			status       TEXT NOT NULL DEFAULT 'pending',
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(requester_id, addressee_id)
		)`,
		// --- groups, channels, messages (Task 3) ---
		`CREATE TABLE IF NOT EXISTS groups (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			owner_id   TEXT REFERENCES users(id),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS group_members (
			group_id TEXT REFERENCES groups(id),
			user_id  TEXT REFERENCES users(id),
			PRIMARY KEY(group_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id         TEXT PRIMARY KEY,
			group_id   TEXT REFERENCES groups(id),
			name       TEXT NOT NULL,
			type       TEXT NOT NULL DEFAULT 'text',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id         TEXT PRIMARY KEY,
			channel_id TEXT REFERENCES channels(id),
			user_id    TEXT REFERENCES users(id),
			text       TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS group_invites (
			id         TEXT PRIMARY KEY,
			group_id   TEXT NOT NULL REFERENCES groups(id),
			inviter_id TEXT NOT NULL REFERENCES users(id),
			invitee_id TEXT REFERENCES users(id),
			code       TEXT UNIQUE,
			status     TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME
		)`,
	}

	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			return err
		}
	}

	// Columns added after the first release; ALTERs are skipped when already present.
	for _, c := range [][3]string{
		{"users", "display_name", "TEXT"},
		{"users", "recovery_hash", "TEXT"},
		{"users", "last_seen_at", "DATETIME"},
		{"users", "invited_by", "TEXT"},
		{"users", "points", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "first_msg_awarded", "INTEGER NOT NULL DEFAULT 0"},
		{"group_members", "role", "TEXT NOT NULL DEFAULT 'member'"},
	} {
		if err := addColumn(database, c[0], c[1], c[2]); err != nil {
			return err
		}
	}

	// The creator of every group is its owner; idempotent backfill for groups that predate roles.
	if _, err := database.Exec(`
		UPDATE group_members SET role = 'owner'
		WHERE role = 'member'
		  AND user_id = (SELECT owner_id FROM groups WHERE groups.id = group_members.group_id)`); err != nil {
		return err
	}

	// Every group gets one voice room; backfills groups created before voice existed.
	_, err := database.Exec(`
		INSERT INTO channels (id, group_id, name, type, created_at)
		SELECT lower(hex(randomblob(16))), g.id, 'Sala de voz', 'voice', CURRENT_TIMESTAMP
		FROM groups g
		WHERE NOT EXISTS (SELECT 1 FROM channels c WHERE c.group_id = g.id AND c.type = 'voice')`)
	return err
}

// addColumn adds col to table unless it already exists, preserving all data.
func addColumn(database *sql.DB, table, col, def string) error {
	rows, err := database.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	exists := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == col {
			exists = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = database.Exec("ALTER TABLE " + table + " ADD COLUMN " + col + " " + def)
	return err
}
