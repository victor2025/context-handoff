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
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	port := flag.String("port", envOr("HANDOFF_PORT", "8765"), "listen port")
	host := flag.String("host", envOr("HANDOFF_HOST", "127.0.0.1"), "listen host")
	dir := flag.String("dir", envOr("HANDOFF_DATA_DIR", "./.handoff-data"), "storage directory")
	cleanup := flag.Int("cleanup-interval", 300, "expiry purge interval in seconds (0 disables)")
	flag.Parse()

	store, err := NewFileStore(*dir)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	if _, err := store.PurgeExpired(time.Now()); err != nil {
		log.Printf("startup purge: %v", err)
	}

	srv := &http.Server{
		Addr:              *host + ":" + *port,
		Handler:           newServer(store).routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[context-handoff] listening on http://%s:%s data=%s", *host, *port, *dir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	var ticker *time.Ticker
	if *cleanup > 0 {
		ticker = time.NewTicker(time.Duration(*cleanup) * time.Second)
		go func() {
			for range ticker.C {
				if n, err := store.PurgeExpired(time.Now()); err != nil {
					log.Printf("purge: %v", err)
				} else if n > 0 {
					log.Printf("purged %d expired records", n)
				}
			}
		}()
	}

	<-stop
	log.Println("shutting down")
	if ticker != nil {
		ticker.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = store.Close()
}
