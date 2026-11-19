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
		// Tasks 3+ will append their CREATE TABLE IF NOT EXISTS here.
	}

	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
