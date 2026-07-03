package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"claudworker/internal/config"
)

// repoEntry is one repository the project may work on. Active=false takes it OUT of agent work; when NO
// repo is active the whole project is deactivated (agents do nothing) via the orchestrator work gate.
type repoEntry struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// repoStore is the per-project, persistent registry of repositories (add/remove/activate on the Git
// page). Seeded once from the config's repos. Its own file under the project's engine home — not shared.
type repoStore struct {
	path string
	mu   sync.Mutex
}

func newRepoStore(projectDir string, seed []config.Repo) *repoStore {
	rs := &repoStore{path: projectDir + "/repos-registry.json"}
	if _, err := os.Stat(rs.path); err != nil { // first run → seed from config (all active)
		var entries []repoEntry
		for _, r := range seed {
			entries = append(entries, repoEntry{Name: r.Name, URL: r.URL, Active: true})
		}
		rs.save(entries)
	}
	return rs
}

func (rs *repoStore) load() []repoEntry {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	b, err := os.ReadFile(rs.path)
	if err != nil {
		return nil
	}
	var out []repoEntry
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func (rs *repoStore) save(entries []repoEntry) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if b, err := json.MarshalIndent(entries, "", "  "); err == nil {
		_ = os.WriteFile(rs.path, b, 0o644)
	}
}

// anyActive reports whether at least one repo is active (project is "on").
func (rs *repoStore) anyActive() bool {
	for _, e := range rs.load() {
		if e.Active {
			return true
		}
	}
	return false
}

func (rs *repoStore) add(name, url string) ([]repoEntry, error) {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return nil, fmt.Errorf("repo name is required")
	}
	entries := rs.load()
	for _, e := range entries {
		if strings.EqualFold(e.Name, name) {
			return nil, fmt.Errorf("repo %q already added", name)
		}
	}
	entries = append(entries, repoEntry{Name: name, URL: url, Active: true})
	rs.save(entries)
	return entries, nil
}

func (rs *repoStore) remove(name string) []repoEntry {
	var out []repoEntry
	for _, e := range rs.load() {
		if !strings.EqualFold(e.Name, name) {
			out = append(out, e)
		}
	}
	rs.save(out)
	return out
}

func (rs *repoStore) setActive(name string, active bool) []repoEntry {
	entries := rs.load()
	for i := range entries {
		if strings.EqualFold(entries[i].Name, name) {
			entries[i].Active = active
		}
	}
	rs.save(entries)
	return entries
}

// deriveOwner extracts the GitHub owner (org/user) from the first configured repo URL, so discovery
// defaults to the PROJECT's own account rather than any hardcoded name.
func deriveOwner(repos []config.Repo) string {
	for _, r := range repos {
		if i := strings.Index(r.URL, "github.com"); i >= 0 {
			rest := strings.TrimLeft(r.URL[i+len("github.com"):], ":/")
			if j := strings.IndexByte(rest, '/'); j > 0 {
				return rest[:j]
			}
		}
	}
	return ""
}

// githubRepos lists every repository owned by `owner` so the Git page can offer them to add. It prefers
// the `gh` CLI with the token env cleared (so gh uses the broader keychain login, which sees all repos),
// and falls back to the REST API with GITHUB_TOKEN. Read-only.
func githubRepos(ctx context.Context, owner string) ([]map[string]any, error) {
	if owner == "" {
		return nil, fmt.Errorf("a GitHub owner (org or user) is required")
	}
	if out, err := githubReposViaGh(ctx, owner); err == nil && len(out) > 0 {
		return out, nil
	}
	return githubReposViaREST(ctx, owner)
}

// githubReposViaGh shells out to `gh repo list` with GH_TOKEN/GITHUB_TOKEN removed so gh authenticates
// via the keychain login (which has full visibility of the account's repos).
func githubReposViaGh(ctx context.Context, owner string) ([]map[string]any, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "list", owner, "--limit", "200", "--json", "name,url,isArchived,isPrivate")
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GH_TOKEN=") || strings.HasPrefix(e, "GITHUB_TOKEN=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var arr []struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Archived  bool   `json:"isArchived"`
		IsPrivate bool   `json:"isPrivate"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(arr))
	for _, r := range arr {
		out = append(out, map[string]any{"name": r.Name, "url": r.URL + ".git", "html_url": r.URL, "archived": r.Archived, "private": r.IsPrivate})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"]) })
	return out, nil
}

// githubReposViaREST lists repos via the GitHub REST API using GITHUB_TOKEN (org then user fallback).
func githubReposViaREST(ctx context.Context, owner string) ([]map[string]any, error) {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	if tok == "" {
		return nil, fmt.Errorf("no GITHUB_TOKEN configured")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	get := func(path string) ([]map[string]any, int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
		req.Header.Set("Authorization", "token "+tok)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, resp.StatusCode, fmt.Errorf("github: http %d", resp.StatusCode)
		}
		var arr []map[string]any
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, resp.StatusCode, err
		}
		return arr, resp.StatusCode, nil
	}
	// Try org repos first, then fall back to a user account.
	arr, _, err := get("/orgs/" + owner + "/repos?per_page=100&sort=updated")
	if err != nil {
		arr, _, err = get("/users/" + owner + "/repos?per_page=100&sort=updated")
	}
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(arr))
	for _, r := range arr {
		out = append(out, map[string]any{
			"name":     r["name"],
			"url":      r["clone_url"],
			"html_url": r["html_url"],
			"archived": r["archived"],
			"private":  r["private"],
		})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"]) })
	return out, nil
}
