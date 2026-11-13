package main

import (
	"context"
	"flag"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/app"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	addr := flag.String("listen", "127.0.0.1:4318", "HTTP listen address")
	db := flag.String("db", "agentlog.db", "SQLite path")
	days := flag.Int("retention-days", 30, "Retention in days since receipt; 0 keeps forever")
	v := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *v {
		log.Print(version)
		return
	}
	if *days < 0 {
		log.Fatal("retention-days must be nonnegative")
	}
	a, e := app.Open(*db, os.Getenv("AGENTLOG_TOKEN"), *days)
	if e != nil {
		log.Fatal(e)
	}
	defer a.DB.Close()
	if e = a.Prune(); e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: *addr, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := a.Prune(); e != nil {
					log.Print(e)
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	log.Printf("agentlog %s listening on %s", version, *addr)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
