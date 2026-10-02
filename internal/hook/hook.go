package hook

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"example.com/git-push-guard/internal/config"
	"example.com/git-push-guard/internal/gitx"
	"example.com/git-push-guard/internal/scan"
)

const ConfigEnv = "GIT_PUSH_GUARD_CONFIG"

func Run(stderr io.Writer, stdin io.Reader) int {
	cfgPath := os.Getenv(ConfigEnv)
	if cfgPath == "" {
		fmt.Fprintln(stderr, "push-guard: missing config path")
		return 1
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "push-guard: invalid configuration: %v\n", err)
		return 1
	}
	repoDir := os.Getenv("GIT_DIR")
	if repoDir == "" {
		repoDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, "push-guard: cannot locate repository")
			return 1
		}
	}
	runner := gitx.New(cfg.GitBin, repoDir)
	var updates []scan.RefUpdate
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			fmt.Fprintf(stderr, "push-guard: malformed ref update record\n")
			return 1
		}
		updates = append(updates, scan.RefUpdate{Old: fields[0], New: fields[1], Ref: fields[2]})
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "push-guard: read ref updates: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ScanTimeoutS)*time.Second)
	defer cancel()
	findings, err := scan.NewEngine(runner, cfg).Run(ctx, updates)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "push-guard: push rejected: scan timed out after %ds\n", cfg.ScanTimeoutS)
		} else {
			fmt.Fprintf(stderr, "push-guard: push rejected: %v\n", err)
		}
		return 1
	}
	if len(findings) > 0 {
		fmt.Fprintf(stderr, "push-guard: push rejected: %d credential finding(s):\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(stderr, "  rule=%s commit=%s path=%q line=%d\n", f.RuleID, f.Commit, f.Path, f.Line)
		}
		return 1
	}
	return 0
}

type Finding = scan.Finding
