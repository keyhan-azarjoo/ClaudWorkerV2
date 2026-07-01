package config

import "testing"

const minimalValid = `
project: demo
engine_home: /tmp/cwv2-home-test
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

func TestParseValidAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(minimalValid))
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if c.Project != "demo" {
		t.Errorf("project = %q, want demo", c.Project)
	}
	// built-in default thresholds must be filled when omitted (owner decision 2 precedence).
	if c.Defaults.AbandonedDays != 30 {
		t.Errorf("abandoned_days default = %d, want 30", c.Defaults.AbandonedDays)
	}
	if c.Defaults.SplitThreshold != 5 {
		t.Errorf("split_threshold default = %d, want 5", c.Defaults.SplitThreshold)
	}
	if c.Defaults.RetryLimits.MaxAttempts != 3 {
		t.Errorf("max_attempts default = %d, want 3", c.Defaults.RetryLimits.MaxAttempts)
	}
	if c.Defaults.LockTTL.Merge != 300 {
		t.Errorf("lock_ttl.merge default = %d, want 300", c.Defaults.LockTTL.Merge)
	}
	if c.Workflow.MaxConcurrent != 3 {
		t.Errorf("max_concurrent default = %d, want 3", c.Workflow.MaxConcurrent)
	}
	if c.Dashboard.Port != 8790 {
		t.Errorf("dashboard.port default = %d, want 8790", c.Dashboard.Port)
	}
}

func TestOverrideThreshold(t *testing.T) {
	src := minimalValid + "\ndefaults:\n  abandoned_days: 7\n  lock_ttl: { issue: 100 }\n"
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Defaults.AbandonedDays != 7 {
		t.Errorf("override abandoned_days = %d, want 7", c.Defaults.AbandonedDays)
	}
	if c.Defaults.LockTTL.Issue != 100 {
		t.Errorf("override lock_ttl.issue = %d, want 100", c.Defaults.LockTTL.Issue)
	}
	// unspecified sibling still gets the built-in default
	if c.Defaults.LockTTL.Merge != 300 {
		t.Errorf("lock_ttl.merge = %d, want default 300", c.Defaults.LockTTL.Merge)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]string{
		"missing project":  "engine_home: /x\njira: { base_url: h, work_jql: q }\ngithub: { commit_identity: { name: n, email: e } }\nrepos: [{name: a, url: u, dev_branch: d, plugin: p}]",
		"missing repos":    "project: p\nengine_home: /x\njira: { base_url: h, work_jql: q }\ngithub: { commit_identity: { name: n, email: e } }",
		"missing identity": "project: p\nengine_home: /x\njira: { base_url: h, work_jql: q }\nrepos: [{name: a, url: u, dev_branch: d, plugin: p}]",
		"missing work_jql": "project: p\nengine_home: /x\njira: { base_url: h }\ngithub: { commit_identity: { name: n, email: e } }\nrepos: [{name: a, url: u, dev_branch: d, plugin: p}]",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
	}
}

func TestUsageGuardResumeMustBeBelowPause(t *testing.T) {
	src := minimalValid + "\nusage_guard: { pause_pct: 80, resume_pct: 90 }\n"
	if _, err := Parse([]byte(src)); err == nil {
		t.Error("expected error when resume_pct > pause_pct")
	}
}

func TestStatusMapMixedScalarAndList(t *testing.T) {
	src := minimalValid + "\n" + `  status_map:
    ready: ["Ready", "Selected for Development"]
    done: "Done"
`
	// note: the extra jira keys must nest under jira; rebuild a full doc instead.
	full := `
project: demo
engine_home: /tmp/x
jira:
  base_url: https://demo.atlassian.net
  work_jql: 'q'
  auth: { user_secret: jira_user, token_secret: jira_token }
  status_map:
    ready: ["Ready", "Selected for Development"]
    done: "Done"
github:
  commit_identity: { name: n, email: e }
repos:
  - { name: app, url: u, dev_branch: development, plugin: flutter }
`
	_ = src
	c, err := Parse([]byte(full))
	if err != nil {
		t.Fatalf("Parse mixed status_map: %v", err)
	}
	if len(c.Jira.StatusMap["ready"]) != 2 {
		t.Errorf("ready = %v, want 2 entries", c.Jira.StatusMap["ready"])
	}
	if len(c.Jira.StatusMap["done"]) != 1 || c.Jira.StatusMap["done"][0] != "Done" {
		t.Errorf("done = %v, want [Done]", c.Jira.StatusMap["done"])
	}
}

// BenchmarkParse is the S0 performance validation: config parse+validate must be sub-millisecond
// (it is on the assembly hot path — prompt/admission decisions must never wait on config I/O).
func BenchmarkParse(b *testing.B) {
	raw := []byte(minimalValid)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSecretNames(t *testing.T) {
	c, err := Parse([]byte(minimalValid))
	if err != nil {
		t.Fatal(err)
	}
	got := c.SecretNames()
	want := map[string]bool{"jira_user": true, "jira_token": true}
	if len(got) != len(want) {
		t.Fatalf("SecretNames = %v, want keys %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected secret name %q", n)
		}
	}
}
