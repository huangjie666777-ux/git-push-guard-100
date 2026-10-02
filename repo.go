package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// repoPath returns the absolute path of the managed bare repository.
func (c *Config) repoPath() string {
	return filepath.Join(c.RepoDir, c.RepoName)
}

// ensureRepo initializes the bare repository on first start and reuses it on
// restart. It refuses to touch a non-empty directory that is not a bare git
// repository, and refuses to overwrite an existing hook we did not create.
func ensureRepo(ctx context.Context, cfg *Config) error {
	if err := os.MkdirAll(cfg.RepoDir, 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}
	repo := cfg.repoPath()
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		if out, err := runGit(ctx, "", nil, "init", "--bare", repo); err != nil {
			return fmt.Errorf("git init --bare: %v: %s", err, out)
		}
	} else {
		out, err := runGit(ctx, "", nil, "-C", repo, "rev-parse", "--is-bare-repository")
		if err != nil || string(bytes.TrimSpace(out)) != "true" {
			return fmt.Errorf("refusing to use %s: not a bare git repository", repo)
		}
	}
	if out, err := runGit(ctx, "", nil, "-C", repo, "config", "http.receivepack", "true"); err != nil {
		return fmt.Errorf("enable http.receivepack: %v: %s", err, out)
	}
	return installHook(cfg)
}

const hookMarker = "git-push-guard pre-receive"

func installHook(cfg *Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.Abs(self)
	hookPath := filepath.Join(cfg.repoPath(), "hooks", "pre-receive")
	content := fmt.Sprintf("#!/bin/sh\n# %s\nexec %q hook --config %q\n",
		hookMarker, self, mustAbs(cfg.configPath))
	if existing, err := os.ReadFile(hookPath); err == nil {
		if !bytes.Contains(existing, []byte(hookMarker)) {
			return fmt.Errorf("refusing to overwrite foreign hook %s", hookPath)
		}
	}
	if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
		return fmt.Errorf("write pre-receive hook: %w", err)
	}
	return nil
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
