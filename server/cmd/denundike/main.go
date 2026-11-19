package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/jemunje/denundike/internal/db"
	"github.com/jemunje/denundike/internal/router"
	"github.com/jemunje/denundike/internal/ws"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./denundike.db"
	}

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	for _, code := range strings.Split(os.Getenv("ADMIN_INVITE_CODE"), ",") {
		if code = strings.TrimSpace(code); code != "" {
			database.Exec( //nolint:errcheck
				`INSERT OR IGNORE INTO invites (code, expires_at) VALUES (?, datetime('now', '+365 days'))`,
				code,
			)
		}
	}

	hub := ws.NewHub()
	go hub.Run()

	port := os.Getenv("PORT")
	addr := os.Getenv("ADDR")
	if addr == "" {
		if port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}

	r := router.New(database, hub)
	log.Printf("denundike listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
