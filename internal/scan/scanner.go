package scan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"example.com/git-push-guard/internal/config"
	"example.com/git-push-guard/internal/gitx"
)

var (
	ErrBudgetExceeded = errors.New("scan byte budget exceeded")
	ErrObjectRead     = errors.New("failed to read quarantined object")
)

type Finding struct {
	RuleID string
	Commit string
	Path   string
	Line   int
}

type blobHit struct {
	ruleID string
	line   int
}

type BatchScanner struct {
	cancel context.CancelFunc
	w      io.WriteCloser
	br     *bufio.Reader
	wg     sync.WaitGroup
	wrErr  error

	budgetLeft int64
	cache      map[string][]blobHit
}

func NewBatchScanner(ctx context.Context, r *gitx.Runner, budget int64) (*BatchScanner, context.Context, error) {
	sctx, cancel := context.WithCancel(ctx)
	c := r.Cmd(sctx, "cat-file", "--batch")
	stdin, err := c.StdinPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	c.Stderr = io.Discard
	if err := c.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start cat-file: %w", err)
	}
	bs := &BatchScanner{
		cancel:     cancel,
		w:          stdin,
		br:         bufio.NewReaderSize(stdout, 1<<20),
		budgetLeft: budget,
		cache:      map[string][]blobHit{},
	}
	bs.wg.Add(1)
	go func() {
		defer bs.wg.Done()
		err := c.Wait()
		if sctx.Err() == nil && err != nil {
			bs.wrErr = err
		}
	}()
	return bs, sctx, nil
}

func (b *BatchScanner) Close() {
	_ = b.w.Close()
	b.cancel()
	b.wg.Wait()
}

// ScanBlob scans one unique blob. Results for repeated OIDs come from cache,
// while the caller retains its own commit/path association.
func (b *BatchScanner) ScanBlob(ctx context.Context, rules []*config.Rule, sha string) ([]blobHit, error) {
	if hits, ok := b.cache[sha]; ok {
		return hits, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(b.w, "%s\n", sha); err != nil {
		return nil, fmt.Errorf("%w: request object: %v", ErrObjectRead, err)
	}
	header, err := b.br.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("%w: read header: %v", ErrObjectRead, err)
	}
	header = bytes.TrimRight(header, "\n")
	parts := strings.Split(string(header), " ")
	if len(parts) < 2 || parts[0] != sha {
		return nil, fmt.Errorf("%w: unexpected reply %q", ErrObjectRead, strconv.Quote(string(header)))
	}
	if parts[1] == "missing" {
		return nil, fmt.Errorf("%w: object %s missing in quarantine", ErrObjectRead, sha)
	}
	if parts[1] != "blob" || len(parts) != 3 {
		return nil, fmt.Errorf("%w: %s is not a blob", ErrObjectRead, sha)
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: bad size", ErrObjectRead)
	}
	if size > b.budgetLeft {
		return nil, ErrBudgetExceeded
	}
	b.budgetLeft -= size
	content := make([]byte, size)
	if _, err := io.ReadFull(b.br, content); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrObjectRead, err)
	}
	trailer := make([]byte, 1)
	if _, err := io.ReadFull(b.br, trailer); err != nil || trailer[0] != '\n' {
		return nil, fmt.Errorf("%w: bad object trailer", ErrObjectRead)
	}
	hits := matchContent(rules, content)
	b.cache[sha] = hits
	return hits, nil
}

func matchContent(rules []*config.Rule, content []byte) []blobHit {
	var hits []blobHit
	lineNo := 0
	for len(content) > 0 {
		var line []byte
		if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
			line = content[:idx]
			content = content[idx+1:]
		} else {
			line = content
			content = nil
		}
		lineNo++
		line = bytes.TrimRight(line, "\r")
		for _, rule := range rules {
			if rule.RE().Match(line) {
				hits = append(hits, blobHit{ruleID: rule.ID, line: lineNo})
			}
		}
	}
	return hits
}

type Engine struct {
	runner *gitx.Runner
	cfg    *config.Config
}

func NewEngine(r *gitx.Runner, cfg *config.Config) *Engine {
	return &Engine{runner: r, cfg: cfg}
}

func (e *Engine) Run(ctx context.Context, updates []RefUpdate) ([]Finding, error) {
	if err := ValidateTags(ctx, e.runner, updates); err != nil {
		return nil, err
	}
	commits, err := NewCommits(ctx, e.runner, updates)
	if err != nil {
		return nil, err
	}
	rules := make([]*config.Rule, len(e.cfg.Rules))
	for i := range e.cfg.Rules {
		rules[i] = &e.cfg.Rules[i]
	}
	bs, sctx, err := NewBatchScanner(ctx, e.runner, e.cfg.MaxScanBytes)
	if err != nil {
		return nil, err
	}
	defer bs.Close()
	var findings []Finding
	blobAssoc := map[string][]FileBlob{}
	var order []string
	for _, commit := range commits {
		files, err := CommitBlobs(sctx, e.runner, commit)
		if err != nil {
			return nil, err
		}
		for _, fb := range files {
			if _, ok := bs.cache[fb.Blob]; !ok {
				if _, seen := blobAssoc[fb.Blob]; !seen {
					order = append(order, fb.Blob)
				}
			}
			blobAssoc[fb.Blob] = append(blobAssoc[fb.Blob], fb)
		}
	}
	for _, sha := range order {
		hits, err := bs.ScanBlob(sctx, rules, sha)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			for _, assoc := range blobAssoc[sha] {
				findings = append(findings, Finding{
					RuleID: hit.ruleID,
					Commit: assoc.Commit,
					Path:   assoc.Path,
					Line:   hit.line,
				})
			}
		}
	}
	return findings, nil
}
