package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"example.com/git-push-guard/internal/config"
	"example.com/git-push-guard/internal/repo"
)

type Server struct {
	httpSrv *http.Server
	ln      net.Listener
}

func New(cfg *config.Config, rp *repo.Repo, configPath string) (*Server, error) {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	cgi := &gitCGI{
		gitBin:      cfg.GitBin,
		projectRoot: rp.Parent,
		validRepo:   rp.Name,
		configPath:  configPath,
		hostPort:    strconv.Itoa(cfg.Port),
	}
	r.Handle("/"+rp.Name+"/info/refs", cgi)
	r.Handle("/"+rp.Name+"/git-upload-pack", cgi)
	r.Handle("/"+rp.Name+"/git-receive-pack", cgi)
	addr := "127.0.0.1:" + strconv.Itoa(cfg.Port)
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	cgi.hostPort = port
	_ = host
	httpSrv := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return context.Background()
		},
		ConnContext: nil,
	}
	return &Server{httpSrv: httpSrv, ln: ln}, nil
}

func (s *Server) Addr() string { return s.ln.Addr().String() }

func (s *Server) Serve() error { return s.httpSrv.Serve(s.ln) }

func (s *Server) Shutdown(ctx context.Context) error { return s.httpSrv.Shutdown(ctx) }
