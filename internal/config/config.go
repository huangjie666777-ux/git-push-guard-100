package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

type Rule struct {
	ID     string          `json:"id"`
	Regexp json.RawMessage `json:"regexp"`
	re     *regexp.Regexp
}

func (r *Rule) RE() *regexp.Regexp { return r.re }

type Config struct {
	Port         int    `json:"port"`
	RepoDir      string `json:"repo_dir"`
	Rules        []Rule `json:"rules"`
	ScanTimeoutS int    `json:"scan_timeout_seconds"`
	MaxScanBytes int64  `json:"max_scan_bytes"`
	GitBin       string `json:"git_bin"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", c.Port)
	}
	if c.RepoDir == "" {
		return nil, fmt.Errorf("repo_dir is required")
	}
	if !filepath.IsAbs(c.RepoDir) {
		ab, err := filepath.Abs(c.RepoDir)
		if err != nil {
			return nil, fmt.Errorf("resolve repo_dir: %w", err)
		}
		c.RepoDir = ab
	}
	if fi, err := os.Stat(c.RepoDir); err == nil {
		if !fi.IsDir() {
			return nil, fmt.Errorf("repo_dir %s is not a directory", c.RepoDir)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat repo_dir: %w", err)
	}
	if c.ScanTimeoutS <= 0 {
		c.ScanTimeoutS = 30
	}
	if c.MaxScanBytes <= 0 {
		c.MaxScanBytes = 256 << 20
	}
	if c.GitBin == "" {
		c.GitBin = "git"
	}
	if len(c.Rules) == 0 {
		return nil, fmt.Errorf("at least one rule is required")
	}
	seenIDs := map[string]bool{}
	for i := range c.Rules {
		r := &c.Rules[i]
		if r.ID == "" {
			return nil, fmt.Errorf("rule %d has empty id", i)
		}
		if seenIDs[r.ID] {
			return nil, fmt.Errorf("duplicate rule id: %s", r.ID)
		}
		seenIDs[r.ID] = true
		if len(r.Regexp) == 0 {
			return nil, fmt.Errorf("rule %s missing regexp", r.ID)
		}
		var pattern string
		if err := json.Unmarshal(r.Regexp, &pattern); err != nil {
			return nil, fmt.Errorf("rule %s regexp must be a string: %w", r.ID, err)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("rule %s invalid regexp %q: %w", r.ID, pattern, err)
		}
		r.re = re
	}
	return &c, nil
}
