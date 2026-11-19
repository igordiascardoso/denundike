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

	// Every group gets one voice room; backfills groups created before voice existed.
	_, err := database.Exec(`
		INSERT INTO channels (id, group_id, name, type, created_at)
		SELECT lower(hex(randomblob(16))), g.id, 'Sala de voz', 'voice', CURRENT_TIMESTAMP
		FROM groups g
		WHERE NOT EXISTS (SELECT 1 FROM channels c WHERE c.group_id = g.id AND c.type = 'voice')`)
	return err
}
