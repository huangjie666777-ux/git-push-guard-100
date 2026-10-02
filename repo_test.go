package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCfg(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Listen:             "127.0.0.1:0",
		RepoDir:            filepath.Join(t.TempDir(), "data"),
		RepoName:           "repo.git",
		MaxScanBytes:       1 << 20,
		ScanTimeoutSeconds: 10,
		scanTimeout:        10 * time.Second,
		Rules:              []Rule{{ID: "k", Pattern: "SECRET"}},
		configPath:         filepath.Join(t.TempDir(), "config.json"),
	}
}

func TestEnsureRepoInitAndReuse(t *testing.T) {
	cfg := testCfg(t)
	if err := ensureRepo(context.Background(), cfg); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.repoPath(), "hooks", "pre-receive")); err != nil {
		t.Fatalf("hook missing: %v", err)
	}
	// Restart reuses the repository.
	if err := ensureRepo(context.Background(), cfg); err != nil {
		t.Fatalf("reuse: %v", err)
	}
}

func TestEnsureRepoRefusesNonRepoDir(t *testing.T) {
	cfg := testCfg(t)
	if err := os.MkdirAll(cfg.repoPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.repoPath(), "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureRepo(context.Background(), cfg); err == nil {
		t.Fatal("expected refusal for non-repository directory")
	}
}
