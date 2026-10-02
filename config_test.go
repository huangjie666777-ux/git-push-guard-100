package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const baseCfg = `{
  "listen": "127.0.0.1:8080",
  "repoDir": "./data",
  "repoName": "repo.git",
  "maxScanBytes": 1024,
  "scanTimeoutSeconds": 5,
  "rules": [%s]
}`

func TestLoadConfigValid(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, sprintf(baseCfg, `{"id":"k","pattern":"AKIA[0-9A-Z]{16}"}`)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rules[0].re == nil || cfg.scanTimeout == 0 {
		t.Fatal("rule not compiled or timeout missing")
	}
}

func TestLoadConfigRejectsBadPattern(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, sprintf(baseCfg, `{"id":"k","pattern":"(["}`))); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestLoadConfigRejectsDuplicateIDs(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, sprintf(baseCfg, `{"id":"a","pattern":"x"},{"id":"a","pattern":"y"}`))); err == nil {
		t.Fatal("expected error for duplicate rule id")
	}
}

func TestLoadConfigRejectsBadRepoName(t *testing.T) {
	body := sprintf(baseCfg, `{"id":"a","pattern":"x"}`)
	body = replaceOnce(body, `"repoName": "repo.git"`, `"repoName": "../evil"`)
	if _, err := loadConfig(writeConfig(t, body)); err == nil {
		t.Fatal("expected error for path-traversal repoName")
	}
}

func TestLoadConfigRejectsEmptyRules(t *testing.T) {
	if _, err := loadConfig(writeConfig(t, sprintf(baseCfg, ""))); err == nil {
		t.Fatal("expected error for empty rules")
	}
}
