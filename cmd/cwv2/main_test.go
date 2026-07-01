package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T) (cfgPath, engineHome string) {
	t.Helper()
	engineHome = t.TempDir()
	cfgPath = filepath.Join(t.TempDir(), "cwv2.yaml")
	src := `
project: demo
engine_home: ` + engineHome + `
jira:
  base_url: https://demo.atlassian.net
  work_jql: 'project = D ORDER BY rank ASC'
  auth: { user_secret: jira_user, token_secret: jira_token }
github:
  user: keyhan-azarjoo
  commit_identity: { name: keyhanazarjoo, email: keyhanazarjoo@gmail.com }
repos:
  - name: app
    url: https://github.com/example/app
    dev_branch: development
    plugin: generic
`
	if err := os.WriteFile(cfgPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath, engineHome
}

func TestRunVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Errorf("version exit = %d, want 0", code)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	if code := run([]string{"frobnicate"}); code != 2 {
		t.Errorf("unknown cmd exit = %d, want 2", code)
	}
}

func TestRunDoctorMissingConfigFlag(t *testing.T) {
	if code := run([]string{"doctor"}); code != 2 {
		t.Errorf("doctor without --config exit = %d, want 2", code)
	}
}

func TestRunDoctorPasses(t *testing.T) {
	cfgPath, _ := writeTempConfig(t)
	// git is expected to be present in the build/test environment; claude/toolchains may be absent
	// (warnings only) -> doctor should PASS (exit 0).
	if code := run([]string{"doctor", "--config", cfgPath}); code != 0 {
		t.Errorf("doctor exit = %d, want 0 (PASS)", code)
	}
}

func TestRunInitCreatesEngineHome(t *testing.T) {
	cfgPath, engineHome := writeTempConfig(t)
	if code := run([]string{"init", "--config", cfgPath}); code != 0 {
		t.Fatalf("init exit = %d, want 0", code)
	}
	proj := filepath.Join(engineHome, "projects", "demo")
	for _, d := range []string{proj, filepath.Join(proj, "knowledge"), filepath.Join(proj, "worktrees")} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("expected %q created by init", d)
		}
	}
}

func TestRunDoctorBadConfigFails(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("project: only\n"), 0o644) // missing required fields
	if code := run([]string{"doctor", "--config", bad}); code != 1 {
		t.Errorf("doctor on invalid config exit = %d, want 1", code)
	}
}
