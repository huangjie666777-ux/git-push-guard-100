package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"example.com/git-push-guard/internal/config"
	"example.com/git-push-guard/internal/hook"
	"example.com/git-push-guard/internal/httpserver"
	"example.com/git-push-guard/internal/repo"
)

func main() {
	if filepath.Base(os.Args[0]) == "pre-receive" {
		code := hook.Run(os.Stderr, os.Stdin)
		os.Exit(code)
	}
	cfgPath := flag.String("config", "config.json", "path to JSON configuration")
	flag.Parse()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	absCfg, err := filepath.Abs(*cfgPath)
	if err != nil {
		log.Fatalf("resolve config path: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	rp, err := repo.Init(ctx, cfg.GitBin, cfg.RepoDir)
	if err != nil {
		log.Fatalf("repository init: %v", err)
	}
	selfExe, err := os.Executable()
	if err != nil {
		log.Fatalf("locate executable: %v", err)
	}
	if err := rp.InstallHook(selfExe); err != nil {
		log.Fatalf("install hook: %v", err)
	}
	srv, err := httpserver.New(cfg, rp, absCfg)
	if err != nil {
		log.Fatalf("start server: %v", err)
	}
	go func() {
		log.Printf("git-push-guard listening on http://%s, repo=%s", srv.Addr(), cfg.RepoDir)
		if err := srv.Serve(); err != nil && err.Error() != "http: Server closed" {
			log.Printf("server: %v", err)
		}
	}()
	<-ctx.Done()
	log.Printf("shutting down")
	sd, sdcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sdcancel()
	if err := srv.Shutdown(sd); err != nil {
		log.Printf("shutdown: %v", err)
	}
	fmt.Fprintln(os.Stderr, "shutdown complete")
}
