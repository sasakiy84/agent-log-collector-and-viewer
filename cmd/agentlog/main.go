package main

import (
	"context"
	"flag"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/app"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import" {
		if e := importMain(os.Args[2:]); e != nil {
			log.Fatal(e)
		}
		return
	}
	if e := serverMain(); e != nil {
		log.Fatal(e)
	}
}

func serverMain() error {
	addr := flag.String("listen", "127.0.0.1:4318", "HTTP listen address")
	db := flag.String("db", "agentlog.db", "SQLite path")
	configFile := flag.String("config", "", "JSON configuration file")
	v := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *v {
		log.Print(version)
		return nil
	}
	watch, interval, e := loadWatchConfig(*configFile)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("agentlog %s starting; preparing database %s", version, *db)
	a, e := app.Start(ctx, *db, os.Getenv("AGENTLOG_TOKEN"))
	if e != nil {
		if ctx.Err() != nil {
			return nil
		}
		return e
	}
	defer a.Close()
	a.Version = version
	srv := &http.Server{Addr: *addr, Handler: a.Handler(), BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	var workers sync.WaitGroup
	if len(watch) > 0 {
		for i := range watch {
			watch[i].Sink = a.IngestLogs
			watch[i].Checkpoint = a.WatchState()
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := importer.Watch(ctx, watch, interval, os.Stderr); err != nil {
				log.Printf("watch: %v", err)
				stop()
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(c); err != nil {
			log.Printf("shutdown: %v", err)
			srv.Close()
		}
	}()
	log.Printf("agentlog %s listening on %s", version, *addr)
	e = srv.ListenAndServe()
	stop()
	workers.Wait()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
