package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// grant is one folder (or file) the worker agents may read/use beyond their single-repo worktree — so a
// cross-repo task (e.g. one whose plan lives in another repo) doesn't fail with "outside the sandbox".
// Scope "always" persists; "once" is the operator's "just this task" choice and auto-expires.
type grant struct {
	Path      string `json:"path"`
	Scope     string `json:"scope"` // always | once
	CreatedAt string `json:"createdAt"`
}

// onceGrantTTL bounds a "just this task" grant so it can't linger forever.
const onceGrantTTL = 3 * time.Hour

// grantStore persists access-grants.json under the project's engine home (own file — not shared).
type grantStore struct {
	path string
	mu   sync.Mutex
}

func newGrantStore(projectDir string) *grantStore {
	return &grantStore{path: filepath.Join(projectDir, "access-grants.json")}
}

func (s *grantStore) loadRaw() []grant {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var out []grant
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *grantStore) save(g []grant) {
	if b, err := json.MarshalIndent(g, "", "  "); err == nil {
		_ = os.WriteFile(s.path, b, 0o644)
	}
}

func grantExpired(g grant) bool {
	if g.Scope != "once" {
		return false
	}
	t, err := time.Parse(time.RFC3339, g.CreatedAt)
	if err != nil {
		return false
	}
	return time.Since(t) > onceGrantTTL
}

// load returns the current grants, dropping expired "once" grants (rewriting the file if any expired).
func (s *grantStore) load() []grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := s.loadRaw()
	kept := make([]grant, 0, len(raw))
	changed := false
	for _, g := range raw {
		if grantExpired(g) {
			changed = true
			continue
		}
		kept = append(kept, g)
	}
	if changed {
		s.save(kept)
	}
	return kept
}

// add grants a path with a scope ("always" default). The path must exist on this machine.
func (s *grantStore) add(path, scope string) ([]grant, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return s.load(), fmt.Errorf("a folder path is required")
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if _, err := os.Stat(path); err != nil {
		return s.load(), fmt.Errorf("path does not exist on this machine: %s", path)
	}
	if scope != "once" {
		scope = "always"
	}
	s.mu.Lock()
	g := s.loadRaw()
	now := time.Now().UTC().Format(time.RFC3339)
	found := false
	for i := range g {
		if g[i].Path == path {
			g[i].Scope = scope
			g[i].CreatedAt = now
			found = true
			break
		}
	}
	if !found {
		g = append(g, grant{Path: path, Scope: scope, CreatedAt: now})
	}
	s.save(g)
	s.mu.Unlock()
	return s.load(), nil
}

func (s *grantStore) remove(path string) []grant {
	s.mu.Lock()
	var out []grant
	for _, g := range s.loadRaw() {
		if g.Path != strings.TrimSpace(path) {
			out = append(out, g)
		}
	}
	s.save(out)
	s.mu.Unlock()
	return s.load()
}

// activePaths returns the granted paths to inject into every worker prompt (nil-safe, expiry-aware).
func (s *grantStore) activePaths() []string {
	var out []string
	for _, g := range s.load() {
		out = append(out, g.Path)
	}
	return out
}
