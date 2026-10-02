package main

import (
	"bytes"
	"context"
	"os/exec"
)

// runGit executes git with an argument vector only (never a shell). When dir
// is empty the process inherits the current environment, which is essential
// inside pre-receive so quarantine object directories remain visible.
func runGit(ctx context.Context, dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, &gitError{args: args, err: err, stderr: stderr.String()}
	}
	return out, nil
}

type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	return "git " + e.args[0] + " failed: " + e.err.Error() + ": " + e.stderr
}
