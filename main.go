package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		fs := flag.NewFlagSet("hook", flag.ExitOnError)
		cfgPath := fs.String("config", "", "path to JSON config")
		_ = fs.Parse(os.Args[2:])
		cfg, err := loadConfig(*cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "git-push-guard: %v\n", err)
			os.Exit(1)
		}
		os.Exit(runHook(cfg, os.Stdin, os.Stderr))
	}

	cfgPath := flag.String("config", "config.json", "path to JSON config")
	flag.Parse()
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	if err := ensureRepo(context.Background(), cfg); err != nil {
		log.Fatalf("repository setup failed: %v", err)
	}

	ln, err := localListener(cfg.Listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: newHandler(cfg)}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("shutting down: terminating git/scan subprocesses")
		killAllGroups()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("serving %s on http://%s/ (repo dir %s)", cfg.RepoName, ln.Addr(), cfg.RepoDir)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
