package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, nil, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// setupRepo creates a repo with one clean commit on master and returns its dir.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "master")
	git(t, dir, "config", "user.email", "t@t")
	git(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "clean")
	return dir
}

// commitTree creates a commit object without any ref pointing at it.
func commitTree(t *testing.T, dir, parent, msg string) string {
	t.Helper()
	tree := git(t, dir, "write-tree")
	args := []string{"commit-tree", tree, "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return git(t, dir, args...)
}

func scanCfg(t *testing.T, maxBytes int64) *Config {
	t.Helper()
	cfg := testCfg(t)
	cfg.MaxScanBytes = maxBytes
	for i := range cfg.Rules {
		cfg.Rules[i].re = mustRe(cfg.Rules[i].Pattern)
	}
	return cfg
}

func mustRe(p string) *regexp.Regexp { return regexp.MustCompile(p) }

func TestScanFindsSecretInMiddleCommit(t *testing.T) {
	dir := setupRepo(t)
	parent := git(t, dir, "rev-parse", "master")
	// Middle commit introduces the secret, tip removes it again.
	if err := os.WriteFile(filepath.Join(dir, "keys.txt"), []byte("token=SECRET123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	mid := commitTree(t, dir, parent, "add secret")
	git(t, dir, "rm", "-q", "--cached", "keys.txt")
	tip := commitTree(t, dir, mid, "remove secret")

	t.Chdir(dir)
	commits, err := newCommitsReachable(context.Background(), []string{tip})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 new commits, got %v", commits)
	}
	s := &scanner{cfg: scanCfg(t, 1<<20), scanned: map[string][]hit{}}
	findings, err := s.scanCommits(context.Background(), commits)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %v", findings)
	}
	f := findings[0]
	if f.RuleID != "k" || f.Commit != mid || f.Path != "keys.txt" || f.Line != 1 {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

func TestScanBudgetExceeded(t *testing.T) {
	dir := setupRepo(t)
	t.Chdir(dir)
	head := git(t, dir, "rev-parse", "master")
	s := &scanner{cfg: scanCfg(t, 2), scanned: map[string][]hit{}}
	if _, err := s.scanCommits(context.Background(), []string{head}); err != errScanBudget {
		t.Fatalf("expected budget error, got %v", err)
	}
}

func TestHookRejectsAndReports(t *testing.T) {
	dir := setupRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x SECRET y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	tip := commitTree(t, dir, git(t, dir, "rev-parse", "master"), "leak")
	t.Chdir(dir)

	cfg := scanCfg(t, 1<<20)
	var stderr bytes.Buffer
	code := runHook(cfg, strings.NewReader(zeroSHA+" "+tip+" refs/heads/feature\n"), &stderr)
	if code == 0 {
		t.Fatal("expected rejection")
	}
	out := stderr.String()
	if !strings.Contains(out, "rule=k") || !strings.Contains(out, tip) || !strings.Contains(out, "a.txt") {
		t.Fatalf("missing report fields: %s", out)
	}
	if strings.Contains(out, "SECRET") {
		t.Fatalf("report leaks secret content: %s", out)
	}
}

func TestHookAllowsCleanPushAndDeletion(t *testing.T) {
	dir := setupRepo(t)
	head := git(t, dir, "rev-parse", "master")
	t.Chdir(dir)
	cfg := scanCfg(t, 1<<20)
	var stderr bytes.Buffer
	in := zeroSHA + " " + head + " refs/heads/copy\n" + head + " " + zeroSHA + " refs/heads/old\n"
	if code := runHook(cfg, strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("clean push rejected: %s", stderr.String())
	}
}

func TestHookRejectsBlobTag(t *testing.T) {
	dir := setupRepo(t)
	t.Chdir(dir)
	blob := git(t, dir, "hash-object", "-w", "--stdin")
	_ = blob
	// Create a lightweight tag ref update pointing at a blob.
	blobSHA := git(t, dir, "rev-parse", "master:clean.txt")
	cfg := scanCfg(t, 1<<20)
	var stderr bytes.Buffer
	code := runHook(cfg, strings.NewReader(zeroSHA+" "+blobSHA+" refs/tags/v1\n"), &stderr)
	if code == 0 {
		t.Fatal("expected rejection of tag pointing to blob")
	}
}
