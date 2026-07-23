// Command lambdamail-protocols is the entry point for the protocols service.
// F0 boots only the health endpoint; SMTP/IMAP/POP3/ManageSieve listeners land in F1/F2.
package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
	"lambdamail/protocols/internal/health"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	handler := health.Handler(func() error { return db.Ping() })

	addr := ":8080"
	log.Printf("lambdamail-protocols listening on %s (health only - F0 scaffold)", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
