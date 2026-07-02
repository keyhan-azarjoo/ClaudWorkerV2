package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssignmentListEmpty(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "cwv2.yaml")
	src := `
project: demo
engine_home: ` + home + `
jira:
  base_url: https://demo.atlassian.net
  work_jql: 'project = D'
  auth: { user_secret: a, token_secret: b }
github:
  commit_identity: { name: keyhanazarjoo, email: keyhanazarjoo@gmail.com }
repos:
  - { name: app, url: u, dev_branch: development, plugin: generic }
`
	if err := os.WriteFile(cfg, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"assignment", "list", "--config", cfg}); code != 0 {
		t.Errorf("assignment list (empty) exit = %d, want 0", code)
	}
}

func TestAssignmentListRequiresConfig(t *testing.T) {
	if code := run([]string{"assignment", "list"}); code != 2 {
		t.Errorf("assignment list without --config exit = %d, want 2", code)
	}
}

func TestAssignmentHelp(t *testing.T) {
	if code := run([]string{"assignment", "help"}); code != 0 {
		t.Errorf("assignment help exit = %d, want 0", code)
	}
}

func TestAssignmentUnknownSub(t *testing.T) {
	if code := run([]string{"assignment", "frobnicate"}); code != 2 {
		t.Errorf("unknown assignment sub exit = %d, want 2", code)
	}
}
