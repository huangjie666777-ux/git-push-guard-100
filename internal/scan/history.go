package scan

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"example.com/git-push-guard/internal/gitx"
)

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type RefUpdate struct {
	Old string
	New string
	Ref string
}

type FileBlob struct {
	Commit string
	Path   string
	Blob   string
}

// ValidateTags rejects tags that do not peel to a commit. Deletions are
// ignored because they introduce no objects.
func ValidateTags(ctx context.Context, r *gitx.Runner, updates []RefUpdate) error {
	for _, u := range updates {
		if u.New == strings.Repeat("0", 40) {
			continue
		}
		if _, _, err := r.Run(ctx, "rev-parse", u.New+"^{commit}"); err != nil {
			return fmt.Errorf("%s (%s) does not resolve to a commit", u.New, u.Ref)
		}
	}
	return nil
}

// NewCommits returns commits reachable from the new tips but not from any
// pre-push ref. In the quarantine environment --all still refers to old refs.
func NewCommits(ctx context.Context, r *gitx.Runner, updates []RefUpdate) ([]string, error) {
	tips := make([]string, 0, len(updates))
	for _, u := range updates {
		if u.New == strings.Repeat("0", 40) {
			continue
		}
		out, _, err := r.Run(ctx, "rev-parse", u.New+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("resolve commit for %s (%s): %w", u.New, u.Ref, err)
		}
		tips = append(tips, strings.TrimSpace(string(out)))
	}
	if len(tips) == 0 {
		return nil, nil
	}
	c := r.Cmd(ctx, "rev-list", "--stdin", "--not", "--all")
	c.Stdin = strings.NewReader(strings.Join(tips, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("rev-list new commits: %w: %s", err, stderr.String())
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits, nil
}

// CommitBlobs lists every blob introduced or changed by the commit, compared
// against each of its parents (merge-safe, root-commit-safe).
func CommitBlobs(ctx context.Context, r *gitx.Runner, commit string) ([]FileBlob, error) {
	out, _, err := r.Run(ctx, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	parents := fields[1:]
	if len(parents) == 0 {
		parents = []string{emptyTree}
	}
	seen := map[string]bool{}
	var result []FileBlob
	for _, parent := range parents {
		raw, _, derr := r.Run(ctx, "diff-tree", "-r", "-z", "--no-commit-id", parent, commit)
		if derr != nil {
			return nil, derr
		}
		for _, entry := range parseDiffRaw(raw) {
			if seen[entry.sha+"\x00"+entry.path] {
				continue
			}
			seen[entry.sha+"\x00"+entry.path] = true
			if entry.sha == strings.Repeat("0", 40) {
				continue
			}
			result = append(result, FileBlob{Commit: commit, Path: entry.path, Blob: entry.sha})
		}
	}
	return result, nil
}

type diffEntry struct {
	sha  string
	path string
}

// parseDiffRaw parses NUL-separated raw diff-tree records:
// :oldmode SP newmode SP oldsha SP newsha TRstatus NUL path [NUL newpath] NUL
func parseDiffRaw(raw []byte) []diffEntry {
	var paired []diffEntry
	segments := bytes.Split(raw, []byte{0})
	for i := 0; i < len(segments); i++ {
		p := segments[i]
		if len(p) == 0 || p[0] != ':' {
			continue
		}
		fields := bytes.SplitN(p, []byte{' '}, 5)
		if len(fields) < 5 {
			continue
		}
		statusFields := bytes.SplitN(fields[4], []byte{'\t'}, 2)
		st := string(statusFields[0])
		pathCount := 1
		if strings.HasPrefix(st, "R") || strings.HasPrefix(st, "C") {
			pathCount = 2
		}
		path := ""
		j := i + 1
		for k := 0; k < pathCount && j < len(segments); k++ {
			path = string(segments[j])
			j++
		}
		paired = append(paired, diffEntry{sha: string(fields[3]), path: path})
	}
	return paired
}
