package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// projectEntry is one registered sub-project: its display name, URL slug and the LOCAL port of its own
// (separate) cwv2 instance. Each sub-project is a fully isolated process — its own config, secrets,
// engine home, tasks and data. Nothing is shared with any other project.
type projectEntry struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	Port int    `json:"port"`
}

// projectsRegistryPath resolves the shared registry of sub-projects (config only: names + ports, never
// project data or secrets), so any instance may read it to render the switcher.
func projectsRegistryPath(cfgPath string) string {
	if p := os.Getenv("CWV2_PROJECTS_FILE"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(cfgPath), "projects.json")
}

// loadProjectsRegistry reads + sanitizes the sub-project registry (missing/invalid file → none).
func loadProjectsRegistry(path string) []projectEntry {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw []projectEntry
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	var out []projectEntry
	for _, e := range raw {
		e.Slug = strings.TrimSpace(e.Slug)
		if e.Slug == "" || e.Port == 0 {
			continue
		}
		if e.Name == "" {
			e.Name = e.Slug
		}
		out = append(out, e)
	}
	return out
}

// projectsList is the switcher model: THIS (default) project + every registered sub-project. base "" is
// the root console; a sub-project lives at /p/<slug>/ on the SAME url.
func projectsList(defaultName string, entries []projectEntry) []map[string]any {
	if defaultName == "" {
		defaultName = "Default"
	}
	out := []map[string]any{{"name": defaultName, "base": ""}}
	for _, e := range entries {
		out = append(out, map[string]any{"name": e.Name, "base": "/p/" + e.Slug})
	}
	return out
}

// projectRouter reverse-proxies /p/<slug>/ to each sub-project's OWN cwv2 instance (a separate process
// on 127.0.0.1:<port>). It reloads the registry live (cached briefly) so a NEWLY added project starts
// serving within seconds WITHOUT restarting the main instance. Same public URL, isolated backend.
type projectRouter struct {
	registryPath string
	mu           sync.Mutex
	cache        []projectEntry
	loadedAt     time.Time
}

func (pr *projectRouter) entries() []projectEntry {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.cache == nil || time.Since(pr.loadedAt) > 5*time.Second {
		pr.cache = loadProjectsRegistry(pr.registryPath)
		pr.loadedAt = time.Now()
	}
	return pr.cache
}

func (pr *projectRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/p/")
	slug := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		slug = rest[:i]
	}
	for _, e := range pr.entries() {
		if e.Slug != slug {
			continue
		}
		prefix := "/p/" + slug
		if r.URL.Path == prefix { // bare /p/<slug> → add slash so relative console assets resolve
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
			return
		}
		target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", e.Port))
		if err != nil {
			http.Error(w, "bad project target", http.StatusBadGateway)
			return
		}
		http.StripPrefix(prefix, httputil.NewSingleHostReverseProxy(target)).ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// listDirs returns the immediate SUB-DIRECTORIES of path (for the folder picker in the Add-Project UI).
// Defaults to $HOME; hidden dirs are skipped. Read-only — never a security-sensitive value.
func listDirs(path string) (map[string]any, error) {
	if strings.TrimSpace(path) == "" {
		path, _ = os.UserHomeDir()
	}
	path = filepath.Clean(path)
	ents, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	parent := filepath.Dir(path)
	return map[string]any{"path": path, "parent": parent, "dirs": dirs}, nil
}

// createProjectInput is the Add-Project form payload.
type createProjectInput struct {
	Name       string `json:"name"`
	Port       int    `json:"port"`
	Repo       string `json:"repo"` // git URL or local folder path
	DevBranch  string `json:"dev_branch"`
	JiraURL    string `json:"jira_url"`
	JiraEmail  string `json:"jira_email"`
	JiraToken  string `json:"jira_token"`
	GithubTok  string `json:"github_token"`
	AccountDir string `json:"account_dir"` // CLAUDE_CONFIG_DIR for this project's account (optional)
	CommitName string `json:"commit_name"` // git author (defaults to the parent project's commit identity)
	CommitMail string `json:"commit_email"`
}

// createProject scaffolds a NEW fully-isolated project: its own dir, config, secrets, engine home and
// web console, registers it in the shared registry (so the dynamic router proxies /p/<slug>/), and
// starts its launchd service. Nothing is shared with any other project.
func createProject(registryPath string, in createProjectInput) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}
	slug := slugify(name)
	if slug == "" {
		return nil, fmt.Errorf("project name must contain letters or digits")
	}
	if in.Port < 1024 || in.Port > 65535 {
		return nil, fmt.Errorf("port must be 1024–65535")
	}
	if strings.TrimSpace(in.Repo) == "" {
		return nil, fmt.Errorf("repo (git URL or local folder) is required")
	}
	if in.DevBranch == "" {
		in.DevBranch = "development"
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cw-"+slug)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%s already exists — pick another name", dir)
	}
	// Reject a slug/port collision with an existing registered project.
	for _, e := range loadProjectsRegistry(registryPath) {
		if e.Slug == slug {
			return nil, fmt.Errorf("a project named %q already exists", name)
		}
		if e.Port == in.Port {
			return nil, fmt.Errorf("port %d is already used by %q", in.Port, e.Name)
		}
	}
	accountDir := in.AccountDir
	if accountDir == "" {
		accountDir = filepath.Join(home, ".cw-accounts", slug)
	}
	proj := strings.ToUpper(slug)

	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		return nil, err
	}
	// Own web console (self-contained).
	_ = exec.Command("cp", "-R", filepath.Join(home, ".cw-live", "web", "ops-console"), filepath.Join(dir, "web")).Run()

	commitName := strings.TrimSpace(in.CommitName)
	if commitName == "" {
		commitName = "ClaudWorker"
	}
	commitMail := strings.TrimSpace(in.CommitMail)
	if commitMail == "" {
		commitMail = "claudworker@localhost"
	}
	cfg := fmt.Sprintf(`project: %s
engine_home: %s/home
github:
  commit_identity: { name: %q, email: %q }
repos:
  - name: repo
    dev_branch: %s
    plugin: generic
    url: %s
accounts:
  - { name: %s, config_dir: %s }
jira:
  base_url: %s
  work_jql: 'project = %s AND status = "To Do" AND labels = ready ORDER BY priority DESC'
usage_guard: { pause_pct: 95, resume_pct: 80, fail_open: false }
dashboard:
`, slug, dir, commitName, commitMail, in.DevBranch, in.Repo, name, accountDir, in.JiraURL, proj)
	if err := os.WriteFile(filepath.Join(dir, "cwv2.yaml"), []byte(cfg), 0o644); err != nil {
		return nil, err
	}

	secrets := fmt.Sprintf("# Credentials for %s ONLY.\nexport JIRA_EMAIL=%q\nexport JIRA_API_TOKEN=%q\nexport GITHUB_TOKEN=%q\nexport SENTRY_API_BASE='https://sentry.io/api/0'\nexport SENTRY_ORG=''\nexport SENTRY_TOKEN=''\n",
		name, in.JiraEmail, in.JiraToken, in.GithubTok)
	if err := os.WriteFile(filepath.Join(dir, "secrets", "live.env"), []byte(secrets), 0o600); err != nil {
		return nil, err
	}

	run := fmt.Sprintf(`#!/bin/bash
export HOME=%s
export BROWSER="$HOME/bin/no-browser"
export CLAUDE_DISABLE_BROWSER_LOGIN=1 CODEX_DISABLE_BROWSER_LOGIN=1 BROWSER_OPEN=none
export CWV2_PROJECTS_FILE=%q
set -a; source %q; set +a
export PATH="$HOME/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/usr/local/share/dotnet:$HOME/.dotnet/tools"
exec "$HOME/bin/cwv2" serve --config %q --mode live --bind 127.0.0.1:%d --web %q
`, home, registryPath, filepath.Join(dir, "secrets", "live.env"), filepath.Join(dir, "cwv2.yaml"), in.Port, filepath.Join(dir, "web", "ops-console"))
	runPath := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(runPath, []byte(run), 0o755); err != nil {
		return nil, err
	}

	label := "com.claudworker." + slug
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>/usr/bin/caffeinate</string><string>-is</string><string>/bin/bash</string><string>%s</string></array>
  <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s/Library/Logs/cwv2-%s.out</string>
  <key>StandardErrorPath</key><string>%s/Library/Logs/cwv2-%s.log</string>
</dict></plist>
`, label, runPath, home, slug, home, slug)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return nil, err
	}

	// Register in the shared registry so the dynamic router proxies /p/<slug>/ (no main restart).
	reg := loadProjectsRegistry(registryPath)
	reg = append(reg, projectEntry{Name: name, Slug: slug, Port: in.Port})
	if b, err := json.MarshalIndent(reg, "", "  "); err == nil {
		_ = os.WriteFile(registryPath, b, 0o644)
	}

	// Start it (best-effort; if creds are incomplete it will fail to boot and /p/<slug>/ returns 502).
	_ = exec.Command("launchctl", "load", plistPath).Run()

	credsOK := in.JiraURL != "" && in.JiraEmail != "" && in.JiraToken != ""
	return map[string]any{
		"name": name, "slug": slug, "port": in.Port, "base": "/p/" + slug, "dir": dir,
		"started": true, "creds_complete": credsOK,
		"note": "Log in its Claude account: CLAUDE_CONFIG_DIR=" + accountDir + " claude" + (map[bool]string{true: "", false: "  ·  fill " + filepath.Join(dir, "secrets", "live.env") + " then reload the service"}[credsOK]),
	}, nil
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
