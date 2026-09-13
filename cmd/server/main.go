// Command server runs the Recall-DSA web application: a single Go binary
// serving both the API and the HTML (SPEC.md §7).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ZadeNova/recall-dsa/internal/db"
	"github.com/ZadeNova/recall-dsa/internal/httpapi"
	"github.com/ZadeNova/recall-dsa/internal/service"
)

// shutdownTimeout bounds how long we wait for in-flight requests (e.g. a
// grading POST mid-write) to finish once a shutdown signal arrives,
// before giving up and exiting anyway.
const shutdownTimeout = 10 * time.Second

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

	// Timeouts bound how long one connection can tie up a goroutine and a
	// file descriptor. Without them a stalled or half-open connection
	// holds both indefinitely, which matters more on a 4GB Pi than on a
	// dev laptop. WriteTimeout is the generous one: a bulk import of a
	// few hundred rows commits in a single transaction before its
	// response is written.
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// ctx's Done channel closes the moment the process receives SIGINT
	// (Ctrl+C) or SIGTERM (what systemd/Docker send on stop) — see the
	// package doc comment below for why this is the right tool here.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (db=%s tz=%s)", *addr, *dbPath, *tz)
		serverErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		// ListenAndServe only returns early on a real startup/runtime
		// error (e.g. the port is already in use) — a normal shutdown
		// is handled by the ctx.Done() branch below instead.
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	case <-ctx.Done():
		log.Print("shutdown signal received, finishing in-flight requests...")
		stop() // restore default OS behavior: a second Ctrl+C now force-kills immediately

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown timed out: %v", err)
		}
	}
}
