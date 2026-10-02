package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/git-push-guard/internal/config"
	"example.com/git-push-guard/internal/hook"
	"example.com/git-push-guard/internal/httpserver"
	"example.com/git-push-guard/internal/repo"
)

const testConfig = `{
  "port": %d,
  "repo_dir": %q,
  "scan_timeout_seconds": 15,
  "max_scan_bytes": 1048576,
  "rules": [
    {"id": "aws-key", "regexp": "AKIA[0-9A-Z]{16}"},
    {"id": "password", "regexp": "(?i)password\\s*=\\s*\\S+"}
  ]
}
`

type envHarness struct {
	t       *testing.T
	root    string
	repoDir string
	cfgPath string
	port    int
}

func setupHarness(t *testing.T) *envHarness {
	t.Helper()
	root := t.TempDir()
	repoDir := filepath.Join(root, "guard.git")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(testConfig, port, repoDir)), 0o600); err != nil {
		t.Fatal(err)
	}
	return &envHarness{t: t, root: root, repoDir: repoDir, cfgPath: cfgPath, port: port}
}

func (h *envHarness) git(dir string, env []string, args ...string) string {
	h.t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		h.t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}

func (h *envHarness) gitFail(dir string, env []string, args ...string) string {
	h.t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	if err == nil {
		h.t.Fatalf("expected git %v to fail", args)
	}
	return out.String()
}

func (h *envHarness) newWorkRepo(name string) string {
	h.t.Helper()
	dir := filepath.Join(h.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.git(h.root, nil, "clone", h.repoDir, dir)
	h.git(dir, nil, "config", "user.email", "t@example.com")
	h.git(dir, nil, "config", "user.name", "T")
	return dir
}

func (h *envHarness) commitFile(dir, name, content, msg string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.git(dir, nil, "add", "--all")
	h.git(dir, nil, "commit", "-m", msg)
}

func TestPreReceiveBlocksSecretInMiddleCommit(t *testing.T) {
	h := setupHarness(t)
	cfg, err := config.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	rp, err := repo.Init(context.Background(), cfg.GitBin, h.repoDir)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := rp.InstallHook(exe); err != nil {
		t.Fatal(err)
	}
	work := h.newWorkRepo("work")
	h.commitFile(work, "clean.txt", "hello\n", "init")
	h.git(work, nil, "branch", "-M", "main")
	h.git(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "main")

	h.commitFile(work, "mid.txt", "key=AKIAABCDEFGHIJKLMNOP\n", "add secret")
	h.commitFile(work, "mid.txt", "all clean now\n", "remove secret")
	out := h.gitFail(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "main")
	if !strings.Contains(out, "rule=aws-key") || !strings.Contains(out, "path=\"mid.txt\"") || strings.Contains(out, "AKIAABCDEFGHIJKLMNOP") {
		t.Fatalf("unexpected hook output: %s", out)
	}
	head := strings.TrimSpace(h.git(h.repoDir, nil, "rev-parse", "main"))
	if strings.Contains(head, " ") {
		t.Fatalf("repo ref changed after rejected push")
	}
	if len(head) != 40 {
		t.Fatalf("unexpected main ref: %q", head)
	}
}

func TestAnnotatedTagToBlobRejectedAndLightweightTagAccepted(t *testing.T) {
	h := setupHarness(t)
	cfg, _ := config.Load(h.cfgPath)
	rp, _ := repo.Init(context.Background(), cfg.GitBin, h.repoDir)
	exe, _ := os.Executable()
	_ = rp.InstallHook(exe)
	work := h.newWorkRepo("work2")
	h.commitFile(work, "f.txt", "data\n", "init")
	h.git(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "main")
	h.git(work, nil, "tag", "v1")
	h.git(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "v1")
	hCmd := exec.Command("git", "hash-object", "-w", "--stdin", "-t", "blob")
	hCmd.Dir = work
	hCmd.Stdin = strings.NewReader("secretblob\n")
	hashOut, err := hCmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	blob := strings.TrimSpace(string(hashOut))
	h.git(work, nil, "tag", "-a", "-m", "bad", "badtag", blob)
	out := h.gitFail(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "badtag")
	if !strings.Contains(out, "does not resolve to a commit") {
		t.Fatalf("expected non-commit tag rejection, got: %s", out)
	}
}

func TestHTTPEndToEnd(t *testing.T) {
	h := setupHarness(t)
	cfg, _ := config.Load(h.cfgPath)
	rp, err := repo.Init(context.Background(), cfg.GitBin, h.repoDir)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if err := rp.InstallHook(exe); err != nil {
		t.Fatal(err)
	}
	srv, err := httpserver.New(cfg, rp, h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	base := "http://" + srv.Addr()
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("healthz=%d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp2, err := http.Get(base + "/evil/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound && resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("outside repo status=%d", resp2.StatusCode)
	}
	cloneDir := filepath.Join(h.root, "httpclone")
	h.git(h.root, nil, "clone", base+"/"+filepath.Base(h.repoDir), cloneDir)
	h.git(cloneDir, nil, "config", "user.email", "t@example.com")
	h.git(cloneDir, nil, "config", "user.name", "T")
	h.commitFile(cloneDir, "clean.txt", "ok\n", "init")
	h.git(cloneDir, nil, "branch", "-M", "main")
	h.git(cloneDir, nil, "push", "origin", "main")
	h.commitFile(cloneDir, "leak.txt", "password = hunter2\n", "leak")
	out := h.gitFail(cloneDir, nil, "push", "origin", "main")
	if !strings.Contains(out, "rule=password") || strings.Contains(out, "hunter2") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestMergeSideBranchWithSecretRejected(t *testing.T) {
	h := setupHarness(t)
	cfg, _ := config.Load(h.cfgPath)
	rp, _ := repo.Init(context.Background(), cfg.GitBin, h.repoDir)
	exe, _ := os.Executable()
	_ = rp.InstallHook(exe)
	work := h.newWorkRepo("work3")
	h.commitFile(work, "base.txt", "base\n", "init")
	h.git(work, nil, "branch", "-M", "main")
	h.git(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "main")
	h.git(work, nil, "checkout", "-b", "feature")
	h.commitFile(work, "side.txt", "AKIAZZZZZZZZZZZZZZZZ\n", "side secret")
	h.git(work, nil, "checkout", "main")
	h.commitFile(work, "main.txt", "main work\n", "main work")
	h.git(work, nil, "merge", "--no-ff", "feature", "-m", "merge feature")
	out := h.gitFail(work, []string{hook.ConfigEnv + "=" + h.cfgPath}, "push", "origin", "main")
	if !strings.Contains(out, "rule=aws-key") || !strings.Contains(out, "path=\"side.txt\"") {
		t.Fatalf("side branch secret not detected: %s", out)
	}
}

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "pre-receive" {
		os.Exit(hook.Run(os.Stderr, os.Stdin))
	}
	os.Exit(m.Run())
}
