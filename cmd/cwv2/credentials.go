package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"claudworker/internal/config"
	"claudworker/internal/jira"
	"claudworker/internal/resource"
)

// credHealth is the owner-facing CREDENTIAL HEALTH view for the Operations Console. It NEVER holds,
// returns, logs or renders a secret VALUE — only names, source, subsystem, status and pass/fail
// validation results. Secret values live only inside the runtime's secret resolver. (Security policy:
// no reveal, no masked value, no raw output.)
type credHealth struct {
	cfg config.Config
	res *resource.Manager

	mu      sync.Mutex
	results map[string]credResult // by env-var name; validation outcomes only (no values)
}

type credResult struct {
	ok     bool
	detail string // NON-secret detail only (e.g. "authenticated", "http 401")
	at     time.Time
}

func newCredHealth(cfg config.Config, res *resource.Manager) *credHealth {
	return &credHealth{cfg: cfg, res: res, results: map[string]credResult{}}
}

// credKeys is the set of credential env-var NAMES the platform can resolve (from CWV2_CREDENTIAL_KEYS).
// Names are not secrets; values are never read into any response.
func credKeys() []string {
	var out []string
	for _, k := range strings.FieldsFunc(os.Getenv("CWV2_CREDENTIAL_KEYS"), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// subsystemFor maps a credential name to the subsystem that uses it (best-effort, by convention).
func subsystemFor(name string) string {
	n := strings.ToUpper(name)
	switch {
	case strings.HasPrefix(n, "ATLASSIAN"):
		return "Jira"
	case n == "GITHUB_TOKEN" || n == "GH_TOKEN":
		return "Git / GitHub"
	case strings.HasPrefix(n, "AZURE"):
		return "Azure"
	case strings.HasPrefix(n, "SENTRY"):
		return "Sentry (logging)"
	case strings.HasPrefix(n, "FIREBASE") || strings.HasPrefix(n, "GOOGLE_APPLICATION"):
		return "Firebase / Google"
	case strings.HasPrefix(n, "EMQX") || strings.Contains(n, "MQTT"):
		return "MQTT / EMQX"
	case strings.HasPrefix(n, "ANDROID") || strings.HasPrefix(n, "ASC_") || strings.HasPrefix(n, "GOOGLE_PLAY"):
		return "App signing / release"
	case strings.HasPrefix(n, "TELEGRAM"):
		return "Telegram"
	case strings.HasPrefix(n, "HUB_") || strings.HasPrefix(n, "VPS") || strings.HasPrefix(n, "MYSERVER") || strings.HasPrefix(n, "MACMINI") || strings.HasPrefix(n, "WINDOWS"):
		return "Infrastructure (SSH)"
	case strings.Contains(n, "WIFI") || strings.Contains(n, "DEVICE"):
		return "Devices"
	case strings.Contains(n, "WOKWI"):
		return "Firmware / Wokwi"
	case strings.Contains(n, "STEPCA") || strings.Contains(n, "KEYCHAIN") || strings.Contains(n, "VAULT"):
		return "PKI / secrets"
	case strings.HasPrefix(n, "TEST_") || strings.Contains(n, "SIM"):
		return "Testing / simulator"
	default:
		return "—"
	}
}

// secretRow reports one credential's HEALTH — never its value.
func (c *credHealth) secretRow(name string) map[string]any {
	present := os.Getenv(name) != ""
	status := "Missing"
	source := "—"
	if present {
		status = "Resolved"
		source = "Environment (bridge)"
	}
	row := map[string]any{"name": name, "source": source, "subsystem": subsystemFor(name), "status": status, "validation": "", "last_validated": ""}
	c.mu.Lock()
	r, ok := c.results[name]
	c.mu.Unlock()
	if ok {
		row["last_validated"] = r.at.UTC().Format(time.RFC3339)
		row["detail"] = r.detail
		if r.ok {
			row["validation"] = "ok"
		} else {
			row["validation"] = "failed"
			if present {
				row["status"] = "Invalid"
			}
		}
	}
	return row
}

// snapshot is the value-free health view: Claude accounts + per-secret status/source/subsystem.
func (c *credHealth) snapshot() map[string]any {
	accounts := []map[string]any{}
	if c.res != nil {
		for _, r := range c.res.List(resource.Filter{Kind: resource.KindClaudeAccount}) {
			accounts = append(accounts, map[string]any{
				"id": r.ID, "name": r.Name, "model": r.Labels["model"],
				"health": string(r.Health), "subsystem": "Runtime (Claude)",
			})
		}
	}
	rows := []map[string]any{}
	for _, k := range credKeys() {
		rows = append(rows, c.secretRow(k))
	}
	return map[string]any{"accounts": accounts, "secrets": rows}
}

// validate confirms credentials WORK without exposing them: live checks for Jira + GitHub, presence
// for the rest. Only pass/fail + timestamps are stored/returned — no secret value.
func (c *credHealth) validate(ctx context.Context) map[string]any {
	now := time.Now()

	jok, jdetail := c.validateJira(ctx)
	for _, k := range []string{"ATLASSIAN_TOKEN", "ATLASSIAN_EMAIL", "ATLASSIAN_SITE"} {
		if os.Getenv(k) != "" {
			c.set(k, jok, jdetail, now)
		}
	}
	gok, gdetail := validateGitHub(ctx)
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if os.Getenv(k) != "" {
			c.set(k, gok, gdetail, now)
		}
	}
	// Presence validation for everything else (can confirm resolvable, not semantics).
	for _, k := range credKeys() {
		c.mu.Lock()
		_, done := c.results[k]
		c.mu.Unlock()
		if done {
			continue
		}
		if os.Getenv(k) != "" {
			c.set(k, true, "present (resolved)", now)
		} else {
			c.set(k, false, "missing", now)
		}
	}
	return c.snapshot()
}

func (c *credHealth) set(name string, ok bool, detail string, at time.Time) {
	c.mu.Lock()
	c.results[name] = credResult{ok: ok, detail: detail, at: at}
	c.mu.Unlock()
}

// validateJira resolves the Jira credentials internally and calls the health endpoint. The token is
// used only to authenticate; it is never returned or logged.
func (c *credHealth) validateJira(ctx context.Context) (bool, string) {
	if c.cfg.Jira.Auth.TokenSecret == "" {
		return false, "not configured"
	}
	email, token, err := jiraCreds(c.cfg)
	if err != nil {
		return false, "credentials unresolved"
	}
	if _, err := jira.New(c.cfg.Jira.BaseURL, email, token).Health(ctx); err != nil {
		return false, "authentication failed"
	}
	return true, "authenticated"
}

// taskAgentCounts returns, per issue key, how many worker/subagent processes (claude or codex) are
// currently running in that task's worktree — the LIVE "agents working on this task" number for the
// dashboard boxes. It reads process working directories via lsof and matches ".../worktrees/<repo>/
// <ISSUE>". Best-effort: returns an empty map if lsof is unavailable.
func taskAgentCounts() map[string]int {
	counts := map[string]int{}
	out, err := exec.Command("lsof", "-a", "-c", "claude", "-c", "codex", "-d", "cwd", "-Fn").Output()
	if err != nil {
		return counts
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		p := line[1:]
		i := strings.Index(p, "/worktrees/")
		if i < 0 {
			continue
		}
		parts := strings.Split(p[i+len("/worktrees/"):], "/")
		if len(parts) >= 2 && parts[1] != "" { // parts[0]=repo, parts[1]=issue key
			counts[parts[1]]++
		}
	}
	return counts
}

// projectKeyFromJQL extracts the project key from a `project = KEY ...` JQL, defaulting to SCRUM.
func projectKeyFromJQL(jql string) string {
	low := strings.ToLower(jql)
	i := strings.Index(low, "project")
	if i < 0 {
		return "SCRUM"
	}
	rest := jql[i+len("project"):]
	rest = strings.TrimLeft(rest, " =\t")
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		end = len(rest)
	}
	key := strings.Trim(rest[:end], `"'`)
	if key == "" {
		return "SCRUM"
	}
	return key
}

// jiraBacklog returns the open board for the project (not just ready-to-work), so the console shows
// the real tasks. Read-only; no secret in the result.
func jiraBacklog(ctx context.Context, cli *jira.Client, projectKey string, includeAll bool, search string) (any, error) {
	if cli == nil {
		return []any{}, nil
	}
	if projectKey == "" {
		projectKey = "SCRUM"
	}
	// Build the JQL from valid clauses. Default: actionable tasks only (not Done/cancelled). includeAll
	// shows EVERY status (for bulk clean-up). Marketing is ALWAYS excluded (owner rule — agents don't
	// touch marketing, so it never appears on the board).
	// Paginate up to 500 so the board shows the REAL count (there can be hundreds of actionable tickets),
	// not a misleading first-100. The status filter + search narrow it down.
	limit := 500
	jql := fmt.Sprintf("project = %s", projectKey)
	if !includeAll {
		jql += " AND statusCategory != Done AND status not in (Cancel, Cancelled, Canceled)"
	}
	if s := strings.TrimSpace(search); s != "" {
		jql += fmt.Sprintf(" AND summary ~ %q", s)
		limit = 500
	}
	jql += ` AND (labels != marketing OR labels is EMPTY) AND summary !~ "marketing" ORDER BY priority DESC, updated DESC`
	issues, err := cli.SearchAll(ctx, jql, []string{"summary", "status", "priority", "labels"}, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(issues))
	for _, iss := range issues {
		prio := ""
		if iss.Fields.Priority != nil {
			prio = iss.Fields.Priority.Name
		}
		ready := false
		for _, l := range iss.Fields.Labels {
			if l == "ready" {
				ready = true
			}
		}
		out = append(out, map[string]any{
			"key": iss.Key, "summary": iss.Fields.Summary, "status": iss.Fields.Status.Name,
			"priority": prio, "labels": iss.Fields.Labels, "ready": ready,
		})
	}
	return out, nil
}

// validateGitHub checks the token against the GitHub API. Only the HTTP result is reported.
func validateGitHub(ctx context.Context) (bool, string) {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	if tok == "" {
		return false, "missing"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return false, "request error"
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false, "network error"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, "authenticated"
	}
	return false, fmt.Sprintf("http %d", resp.StatusCode)
}
