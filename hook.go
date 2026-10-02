package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

const zeroSHA = "0000000000000000000000000000000000000000"

// runHook implements the pre-receive hook. It reads "<old> <new> <ref>" lines
// from stdin, scans all newly introduced commits in the quarantine area, and
// exits non-zero (rejecting the whole push before any ref is updated) on any
// finding or error.
func runHook(cfg *Config, stdin io.Reader, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.scanTimeout)
	defer cancel()

	lines, err := readRefUpdates(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "git-push-guard: cannot read ref updates: %v\n", err)
		return 1
	}

	var tips []string
	for _, u := range lines {
		if u.new == zeroSHA {
			continue // deletion introduces no commits
		}
		typ, err := objectType(ctx, u.new)
		if err != nil {
			fmt.Fprintf(stderr, "git-push-guard: cannot inspect %s: %v\n", u.ref, err)
			return 1
		}
		switch typ {
		case "commit":
			tips = append(tips, u.new)
		case "tag":
			// Annotated tag: only accept tags pointing to commits.
			if _, err := runGit(ctx, "", nil, "rev-parse", "--verify", u.new+"^{commit}"); err != nil {
				fmt.Fprintf(stderr, "git-push-guard: rejecting tag %s: not pointing to a commit\n", u.ref)
				return 1
			}
			tips = append(tips, u.new)
		default:
			fmt.Fprintf(stderr, "git-push-guard: rejecting ref %s: unexpected object type %s\n", u.ref, typ)
			return 1
		}
	}
	if len(tips) == 0 {
		return 0
	}

	commits, err := newCommitsReachable(ctx, tips)
	if err != nil {
		fmt.Fprintf(stderr, "git-push-guard: history traversal failed: %v\n", err)
		return 1
	}
	s := &scanner{cfg: cfg, scanned: map[string][]hit{}, blobCommit: map[string]string{}}
	findings, err := s.scanCommits(ctx, commits)
	if err != nil {
		fmt.Fprintf(stderr, "git-push-guard: scan failed, rejecting push: %v\n", err)
		return 1
	}
	if len(findings) > 0 {
		fmt.Fprintf(stderr, "git-push-guard: push rejected, %d credential finding(s):\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(stderr, "  %s\n", f.String())
		}
		return 1
	}
	return 0
}

type refUpdate struct {
	old, new, ref string
}

func readRefUpdates(r io.Reader) ([]refUpdate, error) {
	var updates []refUpdate
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed ref update line %q", sc.Text())
		}
		updates = append(updates, refUpdate{old: fields[0], new: fields[1], ref: fields[2]})
	}
	return updates, sc.Err()
}

func objectType(ctx context.Context, sha string) (string, error) {
	out, err := runGit(ctx, "", nil, "cat-file", "-t", sha)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
