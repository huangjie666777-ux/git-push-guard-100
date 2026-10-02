package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/go-chi/chi/v5"
)

// procGroups tracks live child process groups so shutdown and client
// disconnects can terminate git and scanning subprocesses.
var procGroups = struct {
	sync.Mutex
	set map[int]struct{}
}{set: map[int]struct{}{}}

func trackGroup(pgid int) {
	procGroups.Lock()
	procGroups.set[pgid] = struct{}{}
	procGroups.Unlock()
}

func untrackGroup(pgid int) {
	procGroups.Lock()
	delete(procGroups.set, pgid)
	procGroups.Unlock()
}

func killGroup(pgid int) {
	// Negative PID targets the whole process group; no shell involved.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func killAllGroups() {
	procGroups.Lock()
	defer procGroups.Unlock()
	for pgid := range procGroups.set {
		killGroup(pgid)
	}
}

// newHandler builds the HTTP router. Only the configured repository name is
// served, so nothing outside the configured repo directory is reachable.
func newHandler(cfg *Config) http.Handler {
	r := chi.NewRouter()
	r.Get("/{repo}", func(w http.ResponseWriter, req *http.Request) {
		serveBackend(cfg, w, req)
	})
	r.Get("/{repo}/*", func(w http.ResponseWriter, req *http.Request) {
		serveBackend(cfg, w, req)
	})
	r.Post("/{repo}/*", func(w http.ResponseWriter, req *http.Request) {
		serveBackend(cfg, w, req)
	})
	return r
}

// serveBackend proxies the request to git-http-backend via CGI using an
// argument vector (no shell). The child runs in its own process group which
// is killed when the request context is cancelled (client disconnect or
// server shutdown).
func serveBackend(cfg *Config, w http.ResponseWriter, req *http.Request) {
	repo := chi.URLParam(req, "repo")
	if repo != cfg.RepoName {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	pathInfo := "/" + repo
	if rest := chi.URLParam(req, "*"); rest != "" {
		pathInfo += "/" + rest
	}

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "http-backend")
	cmd.Env = []string{
		"GIT_PROJECT_ROOT=" + cfg.RepoDir,
		"GIT_HTTP_EXPORT_ALL=1",
		"PATH_INFO=" + pathInfo,
		"REQUEST_METHOD=" + req.Method,
		"QUERY_STRING=" + req.URL.RawQuery,
		"CONTENT_TYPE=" + req.Header.Get("Content-Type"),
		"REMOTE_ADDR=" + req.RemoteAddr,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	if cl := req.Header.Get("Content-Length"); cl != "" {
		cmd.Env = append(cmd.Env, "CONTENT_LENGTH="+cl)
	} else if req.ContentLength > 0 {
		cmd.Env = append(cmd.Env, "CONTENT_LENGTH="+strconv.FormatInt(req.ContentLength, 10))
	}
	cmd.Stdin = req.Body
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "backend error", http.StatusInternalServerError)
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		http.Error(w, "backend error", http.StatusInternalServerError)
		return
	}
	pgid := cmd.Process.Pid
	trackGroup(pgid)
	defer untrackGroup(pgid)
	go func() {
		<-ctx.Done()
		killGroup(pgid)
	}()

	br := bufio.NewReader(stdout)
	status := http.StatusOK
	header := http.Header{}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			log.Printf("git-http-backend: bad response: %v", err)
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.EqualFold(k, "Status") {
			if n, err := strconv.Atoi(strings.Fields(v)[0]); err == nil {
				status = n
			}
			continue
		}
		header.Add(k, v)
	}
	for k, vs := range header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, br)
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Printf("git-http-backend exited: %v", err)
	}
}

// localListener binds only to a loopback address.
func localListener(addr string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host == "" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("listen address %q is not loopback; refusing", addr)
	}
	return net.Listen("tcp", net.JoinHostPort(host, port))
}
