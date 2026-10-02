package httpserver

import (
	"context"

	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"

	"example.com/git-push-guard/internal/hook"
)

// gitCGI bridges requests to git-http-backend without using sh -c.
type gitCGI struct {
	gitBin      string
	projectRoot string
	validRepo   string
	configPath  string
	hostPort    string
}

func (g *gitCGI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rel := strings.TrimPrefix(req.URL.Path, "/")
	clean := path.Clean("/" + rel)
	var repoPath, pathInfo string
	switch {
	case clean == "/"+g.validRepo+"/info/refs" || clean == "/"+g.validRepo+"/git-upload-pack" || clean == "/"+g.validRepo+"/git-receive-pack":
		repoPath = "/" + g.validRepo
		pathInfo = clean
	case strings.HasPrefix(clean, "/"+g.validRepo+"/info/refs"):
		repoPath = "/" + g.validRepo
		pathInfo = "/" + g.validRepo + "/info/refs"
	default:
		http.Error(w, "forbidden: repository not served", http.StatusForbidden)
		return
	}
	if strings.Contains(repoPath, "..") || repoPath != "/"+g.validRepo {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	c := exec.CommandContext(ctx, g.gitBin, "http-backend")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
	env := []string{
		"GATEWAY_INTERFACE=CGI/1.1",
		"GIT_PROJECT_ROOT=" + g.projectRoot,
		"GIT_HTTP_EXPORT_ALL=1",
		"PATH_INFO=" + pathInfo,
		"SCRIPT_NAME=" + repoPath,
		"REQUEST_METHOD=" + req.Method,
		"QUERY_STRING=" + req.URL.RawQuery,
		"SERVER_PROTOCOL=HTTP/1.1",
		"SERVER_NAME=127.0.0.1",
		"SERVER_PORT=" + g.hostPort,
		"REMOTE_ADDR=127.0.0.1",
		"REMOTE_USER=anonymous",
		"CONTENT_TYPE=" + req.Header.Get("Content-Type"),
		"CONTENT_LENGTH=" + req.Header.Get("Content-Length"),
		hook.ConfigEnv + "=" + g.configPath,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	}
	for k, v := range req.Header {
		if len(v) > 0 {
			u := strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
			env = append(env, "HTTP_"+u+"="+v[0])
		}
	}
	c.Env = env
	stdin, err := c.StdinPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		http.Error(w, "failed to start git backend", http.StatusInternalServerError)
		return
	}
	go func() {
		if req.Body != nil {
			_, _ = io.Copy(stdin, req.Body)
		}
		_ = stdin.Close()
	}()
	hr := newHeaderReader(stdout)
	status := 200
	hdr := http.Header{}
	if err := hr.ReadHeaders(); err != nil {
		cancel()
		_ = c.Wait()
		http.Error(w, "bad response from git backend", http.StatusBadGateway)
		return
	}
	for k, vs := range hr.Header {
		for _, v := range vs {
			hdr.Add(k, v)
		}
	}
	if s := hr.Status; s > 0 {
		status = s
	}
	if s := hdr.Get("Status"); s != "" {
		if code, _, ok := strings.Cut(s, " "); ok {
			if n, err := parseInt(code); err == nil {
				status = n
			}
		}
	}
	hdr.Del("Status")
	for k, vs := range hdr {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, hr)
	_ = c.Wait()
}

func parseInt(s string) (int, error) {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errBadStatus
		}
		n = n*10 + int(ch-'0')
	}
	if s == "" {
		return 0, errBadStatus
	}
	return n, nil
}

var errBadStatus = errors.New("bad status")
