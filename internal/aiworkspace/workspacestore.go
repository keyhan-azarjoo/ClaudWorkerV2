package aiworkspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Workspace groups repos/folders and per-workspace optimizer choices. (This is the AI Workspace notion
// of a "project" — named "Workspace" to avoid colliding with cwv2's isolated top-level Projects.) The
// "current" workspace drives the Dashboard's workspace tile.
type Workspace struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Repos      []string `json:"repos"`
	Folders    []string `json:"folders"`
	Notes      string   `json:"notes,omitempty"`
	Optimizers []string `json:"optimizers"` // optimizer ids active for this workspace
	Current    bool     `json:"current"`
	CreatedAt  string   `json:"createdAt"`
}

// workspaceStore persists aiw-workspaces.json.
type workspaceStore struct {
	path string
	mu   sync.Mutex
}

func newWorkspaceStore(dir string) *workspaceStore {
	return &workspaceStore{path: filepath.Join(dir, "aiw-workspaces.json")}
}

func (s *workspaceStore) load() []Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Workspace
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (s *workspaceStore) save(ws []Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, err := json.MarshalIndent(ws, "", "  "); err == nil {
		_ = os.WriteFile(s.path, b, 0o644)
	}
}

func (s *workspaceStore) add(name string) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, fmt.Errorf("workspace name is required")
	}
	ws := s.load()
	w := Workspace{ID: nextID("ws"), Name: name, Optimizers: []string{}, Repos: []string{}, Folders: []string{}, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if len(ws) == 0 {
		w.Current = true // first workspace is current
	}
	ws = append(ws, w)
	s.save(ws)
	return w, nil
}

func (s *workspaceStore) find(ws []Workspace, id string) int {
	for i := range ws {
		if ws[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *workspaceStore) update(w Workspace) ([]Workspace, error) {
	ws := s.load()
	i := s.find(ws, w.ID)
	if i < 0 {
		return ws, fmt.Errorf("unknown workspace %q", w.ID)
	}
	// Preserve Current + CreatedAt; update the editable fields.
	w.Current = ws[i].Current
	w.CreatedAt = ws[i].CreatedAt
	if strings.TrimSpace(w.Name) == "" {
		w.Name = ws[i].Name
	}
	if w.Repos == nil {
		w.Repos = []string{}
	}
	if w.Folders == nil {
		w.Folders = []string{}
	}
	if w.Optimizers == nil {
		w.Optimizers = []string{}
	}
	ws[i] = w
	s.save(ws)
	return ws, nil
}

func (s *workspaceStore) remove(id string) []Workspace {
	ws := s.load()
	var out []Workspace
	removedCurrent := false
	for _, w := range ws {
		if w.ID == id {
			removedCurrent = w.Current
			continue
		}
		out = append(out, w)
	}
	if removedCurrent && len(out) > 0 {
		out[0].Current = true
	}
	s.save(out)
	return out
}

func (s *workspaceStore) setCurrent(id string) []Workspace {
	ws := s.load()
	for i := range ws {
		ws[i].Current = ws[i].ID == id
	}
	s.save(ws)
	return ws
}

func (s *workspaceStore) current() (Workspace, bool) {
	for _, w := range s.load() {
		if w.Current {
			return w, true
		}
	}
	return Workspace{}, false
}
