package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type Runner struct {
	GitBin string
	Repo   string
}

func New(gitBin, repo string) *Runner {
	return &Runner{GitBin: gitBin, Repo: repo}
}

func (r *Runner) gitEnv() []string {
	return append(os.Environ(),
		"GIT_DIR="+r.Repo,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

func (r *Runner) Cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, r.GitBin, args...)
	c.Env = r.gitEnv()
	c.Dir = r.Repo
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
	return c
}

func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	c := r.Cmd(ctx, args...)
	stdout, err := c.Output()
	var stderr []byte
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = ee.Stderr
	}
	if err != nil {
		return stdout, stderr, fmt.Errorf("git %v: %w: %s", args, err, string(stderr))
	}
	return stdout, stderr, nil
}
