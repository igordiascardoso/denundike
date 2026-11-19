package main

import (
	"log"
	"net/http"
	"os"

	"github.com/jemunje/denundike/internal/db"
	"github.com/jemunje/denundike/internal/router"
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

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	r := router.New(database)
	log.Printf("denundike listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
