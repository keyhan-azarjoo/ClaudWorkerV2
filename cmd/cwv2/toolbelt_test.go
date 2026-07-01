package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-b", "development").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestGitCLIFlow(t *testing.T) {
	repo := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// commit
	if code := run([]string{"git", "commit", "--repo", repo, "--message", "init"}); code != 0 {
		t.Fatalf("git commit exit = %d", code)
	}
	// rev
	if code := run([]string{"git", "rev", "--repo", repo}); code != 0 {
		t.Errorf("git rev exit = %d", code)
	}
	// clean (should be clean after commit)
	if code := run([]string{"git", "clean", "--repo", repo}); code != 0 {
		t.Errorf("git clean exit = %d", code)
	}
	// branch-create idempotent
	if code := run([]string{"git", "branch-create", "--repo", repo, "--name", "agent/x", "--base", "development"}); code != 0 {
		t.Errorf("branch-create exit = %d", code)
	}
	if code := run([]string{"git", "branch-create", "--repo", repo, "--name", "agent/x", "--base", "development"}); code != 0 {
		t.Errorf("idempotent branch-create exit = %d", code)
	}
}

func TestGitCLIErrorOnNonRepo(t *testing.T) {
	if code := run([]string{"git", "rev", "--repo", filepath.Join(t.TempDir(), "nope")}); code != 1 {
		t.Errorf("git rev on non-repo exit = %d, want 1", code)
	}
}

func TestGitCLIUnknownSub(t *testing.T) {
	if code := run([]string{"git", "frobnicate"}); code != 2 {
		t.Errorf("unknown git sub exit = %d, want 2", code)
	}
}

func TestGitCLIHelp(t *testing.T) {
	if code := run([]string{"git", "help"}); code != 0 {
		t.Errorf("git help exit = %d, want 0", code)
	}
}

func writeJiraConfig(t *testing.T, baseURL string) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "cwv2.yaml")
	src := `
project: demo
engine_home: ` + t.TempDir() + `
jira:
  base_url: ` + baseURL + `
  work_jql: 'project = D'
  auth: { user_secret: cwv2_absent_user_zzz, token_secret: cwv2_absent_token_zzz }
github:
  commit_identity: { name: keyhanazarjoo, email: keyhanazarjoo@gmail.com }
repos:
  - { name: app, url: u, dev_branch: development, plugin: generic }
`
	if err := os.WriteFile(cfg, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestJiraCLIRequiresConfig(t *testing.T) {
	if code := run([]string{"jira", "health"}); code != 2 {
		t.Errorf("jira health without --config exit = %d, want 2", code)
	}
}

func TestJiraCLIUnresolvableSecretFails(t *testing.T) {
	// No env secrets set -> auth cannot be resolved -> structured error, exit 1.
	cfg := writeJiraConfig(t, "https://demo.atlassian.net")
	if code := run([]string{"jira", "health", "--config", cfg}); code != 1 {
		t.Errorf("jira health with unresolvable secret exit = %d, want 1", code)
	}
}

func TestJiraCLIHelp(t *testing.T) {
	if code := run([]string{"jira", "help"}); code != 0 {
		t.Errorf("jira help exit = %d, want 0", code)
	}
}
