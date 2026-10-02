package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"example.com/git-push-guard/internal/gitx"
)

type Repo struct {
	Dir    string
	Name   string
	Parent string
	Runner *gitx.Runner
}

func Init(ctx context.Context, gitBin, dir string) (*Repo, error) {
	dir = filepath.Clean(dir)
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return nil, fmt.Errorf("repo path %s exists and is not a directory", dir)
		}
		head := filepath.Join(dir, "HEAD")
		if hfi, herr := os.Stat(head); herr == nil && !hfi.IsDir() {
			if err := verifyBare(ctx, gitBin, dir); err != nil {
				return nil, err
			}
		} else {
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				return nil, fmt.Errorf("refusing to overwrite non-repository directory: %s", dir)
			}
			if err := createBare(ctx, gitBin, dir); err != nil {
				return nil, err
			}
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create repo dir: %w", err)
		}
		if err := createBare(ctx, gitBin, dir); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("stat repo: %w", err)
	}
	if err := verifyBare(ctx, gitBin, dir); err != nil {
		return nil, err
	}
	name := filepath.Base(dir)
	return &Repo{
		Dir:    dir,
		Name:   name,
		Parent: filepath.Dir(dir),
		Runner: gitx.New(gitBin, dir),
	}, nil
}

func createBare(ctx context.Context, gitBin, dir string) error {
	r := gitx.New(gitBin, dir)
	if _, _, err := r.Run(ctx, "init", "--bare", "--initial-branch=main", dir); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	cfg := [][2]string{
		{"http.receivepack", "true"},
		{"uploadpack.allowFilter", "true"},
		{"receive.denyCurrentBranch", "ignore"},
	}
	for _, kv := range cfg {
		if _, _, err := r.Run(ctx, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func verifyBare(ctx context.Context, gitBin, dir string) error {
	r := gitx.New(gitBin, dir)
	out, _, err := r.Run(ctx, "rev-parse", "--is-bare-repository")
	if err != nil {
		return fmt.Errorf("not a valid git repository: %s: %w", dir, err)
	}
	if string(out) != "true\n" {
		return fmt.Errorf("%s is not a bare repository", dir)
	}
	return nil
}

func (rp *Repo) InstallHook(selfExe string) error {
	hookDir := filepath.Join(rp.Dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}
	dst := filepath.Join(hookDir, "pre-receive")
	raw, err := os.ReadFile(selfExe)
	if err != nil {
		return fmt.Errorf("read own binary: %w", err)
	}
	if err := os.WriteFile(dst, raw, 0o755); err != nil {
		return fmt.Errorf("install hook: %w", err)
	}
	return nil
}
