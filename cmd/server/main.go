// Command server runs the Recall-DSA web application: a single Go binary
// serving both the API and the HTML (SPEC.md §7).
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/db"
	"github.com/ZadeNova/recall-dsa/internal/httpapi"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

func main() {
	dbPath := flag.String("db", "recall.db", "path to the SQLite database file")
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	tz := flag.String("tz", "Asia/Singapore", `timezone used to compute "today" (SPEC.md §4) — set to your own if self-hosting`)
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database %q: %v", *dbPath, err)
	}
	defer conn.Close()

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatalf("load timezone %q: %v", *tz, err)
	}

	svc := service.New(conn, loc)

	srv, err := httpapi.NewServer(svc)
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	log.Printf("listening on %s (db=%s tz=%s)", *addr, *dbPath, *tz)
	if err := http.ListenAndServe(*addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
