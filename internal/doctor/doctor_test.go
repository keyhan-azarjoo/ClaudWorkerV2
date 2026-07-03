package doctor

import (
	"testing"

	"claudworker/internal/config"
	"claudworker/internal/secrets"
)

func testConfig(t *testing.T, engineHome string) *config.Config {
	t.Helper()
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
    plugin: flutter
`
	c, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatalf("config parse: %v", err)
	}
	return c
}

// gitPresentLooker reports git+flutter present but claude absent.
func gitPresentLooker(name string) bool {
	return name == "git" || name == "flutter"
}

func TestRunPassesWhenRequiredToolsPresent(t *testing.T) {
	cfg := testConfig(t, t.TempDir())
	// make secrets resolvable via env so those checks are ok.
	t.Setenv("JIRA_USER", "u")
	t.Setenv("JIRA_TOKEN", "tk")

	rep := Run(cfg, Options{
		Resolver: secrets.NewResolver(),
		LookPath: gitPresentLooker,
	})
	if !rep.OK() {
		t.Fatalf("expected PASS, got checks: %+v", rep.Checks)
	}
	// claude is absent -> should be a warning, not a fail.
	if st := statusOf(rep, "tool:claude"); st != Warn {
		t.Errorf("tool:claude status = %v, want warn", st)
	}
	if st := statusOf(rep, "tool:git"); st != OK {
		t.Errorf("tool:git status = %v, want ok", st)
	}
	if st := statusOf(rep, "plugin:flutter"); st != OK {
		t.Errorf("plugin:flutter status = %v, want ok", st)
	}
}

func TestRunFailsWhenGitMissing(t *testing.T) {
	cfg := testConfig(t, t.TempDir())
	rep := Run(cfg, Options{
		Resolver: secrets.NewResolver(),
		LookPath: func(string) bool { return false }, // nothing present
	})
	if rep.OK() {
		t.Fatal("expected FAIL when git missing")
	}
	if st := statusOf(rep, "tool:git"); st != Fail {
		t.Errorf("tool:git status = %v, want fail", st)
	}
	// missing plugin toolchain must degrade to warn, not fail.
	if st := statusOf(rep, "plugin:flutter"); st != Warn {
		t.Errorf("plugin:flutter status = %v, want warn", st)
	}
}

func TestUnresolvableSecretIsWarnNotFail(t *testing.T) {
	cfg := testConfig(t, t.TempDir())
	// do NOT set env secrets -> unresolvable
	rep := Run(cfg, Options{
		Resolver: secrets.NewResolver(),
		LookPath: gitPresentLooker,
	})
	if st := statusOf(rep, "secret:jira_token"); st != Warn {
		t.Errorf("secret:jira_token status = %v, want warn", st)
	}
	// still overall PASS (git present, secrets only warn)
	if !rep.OK() {
		t.Errorf("expected PASS with only secret warnings, got: %+v", rep.Checks)
	}
}

func statusOf(r *Report, name string) Status {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return ""
}
