package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Finding reports one rule hit. It intentionally never carries the matched
// secret text, only rule ID, commit, path and line number.
type Finding struct {
	RuleID string
	Commit string
	Path   string
	Line   int
}

func (f Finding) String() string {
	return fmt.Sprintf("rule=%s commit=%s path=%s line=%d", f.RuleID, f.Commit, f.Path, f.Line)
}

var errScanBudget = errors.New("scan byte budget exceeded")

// scanner walks new commits and scans blob contents against the rules.
type scanner struct {
	cfg        *Config
	scanned    map[string][]hit // blob sha -> hits (path-less, per line)
	blobCommit map[string]string
	spent      int64
}

type hit struct {
	ruleID string
	line   int
}

// newCommitsReachable lists commits reachable from any of tips but not from
// any pre-existing ref. Must run inside the quarantine environment.
func newCommitsReachable(ctx context.Context, tips []string) ([]string, error) {
	args := []string{"rev-list"}
	args = append(args, tips...)
	args = append(args, "--not", "--all")
	out, err := runGit(ctx, "", nil, args...)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			commits = append(commits, l)
		}
	}
	return commits, nil
}

// scanCommits scans every file of every given commit, reusing results for
// blobs already scanned. It enforces the configured byte budget.
func (s *scanner) scanCommits(ctx context.Context, commits []string) ([]Finding, error) {
	var findings []Finding
	for _, commit := range commits {
		entries, err := listBlobs(ctx, commit)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			hits, err := s.scanBlob(ctx, e.sha)
			if err != nil {
				return nil, err
			}
			for _, h := range hits {
				findings = append(findings, Finding{
					RuleID: h.ruleID,
					Commit: commit,
					Path:   e.path,
					Line:   h.line,
				})
			}
		}
	}
	return findings, nil
}

type blobEntry struct {
	sha  string
	path string
}

// listBlobs lists all blobs of a commit using NUL-delimited output so that
// arbitrary file names (spaces, newlines, quotes) are handled safely.
func listBlobs(ctx context.Context, commit string) ([]blobEntry, error) {
	out, err := runGit(ctx, "", nil, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	var entries []blobEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(string(rec[:tab]))
		// meta: <mode> <type> <sha>
		if len(meta) != 3 || meta[1] != "blob" {
			continue
		}
		entries = append(entries, blobEntry{sha: meta[2], path: string(rec[tab+1:])})
	}
	return entries, nil
}

// scanBlob scans one blob, caching results so identical blobs across commits
// are scanned only once.
func (s *scanner) scanBlob(ctx context.Context, sha string) ([]hit, error) {
	if hits, ok := s.scanned[sha]; ok {
		return hits, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := runGit(ctx, "", nil, "cat-file", "blob", sha)
	if err != nil {
		return nil, err
	}
	s.spent += int64(len(content))
	if s.spent > s.cfg.MaxScanBytes {
		return nil, errScanBudget
	}
	var hits []hit
	lines := bytes.Split(content, []byte("\n"))
	for i, line := range lines {
		for _, r := range s.cfg.Rules {
			if r.re.Match(line) {
				hits = append(hits, hit{ruleID: r.ID, line: i + 1})
			}
		}
	}
	s.scanned[sha] = hits
	return hits, nil
}
