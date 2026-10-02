package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Rule is a single credential-detection rule loaded from the JSON config.
type Rule struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	re      *regexp.Regexp
}

// Config is the server configuration loaded at startup.
type Config struct {
	Listen             string `json:"listen"`
	RepoDir            string `json:"repoDir"`
	RepoName           string `json:"repoName"`
	MaxScanBytes       int64  `json:"maxScanBytes"`
	ScanTimeoutSeconds int    `json:"scanTimeoutSeconds"`
	Rules              []Rule `json:"rules"`

	scanTimeout time.Duration
	configPath  string
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Listen == "" {
		return nil, errors.New("config: listen is required")
	}
	if cfg.RepoDir == "" {
		return nil, errors.New("config: repoDir is required")
	}
	if cfg.RepoName == "" {
		return nil, errors.New("config: repoName is required")
	}
	if !validRepoName(cfg.RepoName) {
		return nil, fmt.Errorf("config: invalid repoName %q", cfg.RepoName)
	}
	if cfg.MaxScanBytes <= 0 {
		return nil, errors.New("config: maxScanBytes must be > 0")
	}
	if cfg.ScanTimeoutSeconds <= 0 {
		return nil, errors.New("config: scanTimeoutSeconds must be > 0")
	}
	if len(cfg.Rules) == 0 {
		return nil, errors.New("config: at least one rule is required")
	}
	seen := map[string]bool{}
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if r.ID == "" {
			return nil, fmt.Errorf("config: rule #%d missing id", i)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("config: duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("config: rule %q: invalid pattern: %w", r.ID, err)
		}
		r.re = re
	}
	cfg.configPath, _ = filepath.Abs(path)
	cfg.scanTimeout = time.Duration(cfg.ScanTimeoutSeconds) * time.Second
	return &cfg, nil
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validRepoName(name string) bool {
	return repoNameRe.MatchString(name) && name != "." && name != ".."
}
