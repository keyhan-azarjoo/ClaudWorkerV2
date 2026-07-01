// Package enginehome manages the per-project engine-home directory layout on disk.
//
// Everything mutable the engine owns lives here (docs/14_Deployment.md, C-6: on the external SSD).
// The layout separates persistent knowledge from temporary execution state (docs/04_ProjectBrain.md,
// docs/12_Database.md): knowledge.db + knowledge/ vs state.db.
package enginehome

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout describes the on-disk paths for one project's engine home.
type Layout struct {
	Root        string // <engine_home>
	EngineDB    string // <engine_home>/engine.db (engine-global; not created here)
	ProjectDir  string // <engine_home>/projects/<project>
	KnowledgeDB string // <project>/knowledge.db      (Knowledge Brain — persistent)
	KnowledgeMD string // <project>/knowledge/         (architecture.md, decisions/, ...)
	StateDB     string // <project>/state.db           (Execution State — temporary)
	Worktrees   string // <project>/worktrees/
	Artifacts   string // <project>/artifacts/
	Logs        string // <engine_home>/logs/
}

// For computes the layout for a project rooted at engineHome. It does not touch the filesystem.
func For(engineHome, project string) Layout {
	proj := filepath.Join(engineHome, "projects", project)
	return Layout{
		Root:        engineHome,
		EngineDB:    filepath.Join(engineHome, "engine.db"),
		ProjectDir:  proj,
		KnowledgeDB: filepath.Join(proj, "knowledge.db"),
		KnowledgeMD: filepath.Join(proj, "knowledge"),
		StateDB:     filepath.Join(proj, "state.db"),
		Worktrees:   filepath.Join(proj, "worktrees"),
		Artifacts:   filepath.Join(proj, "artifacts"),
		Logs:        filepath.Join(engineHome, "logs"),
	}
}

// dirs are the directories Ensure creates (DB files are created by later subsystems).
func (l Layout) dirs() []string {
	return []string{
		l.Root,
		filepath.Dir(l.ProjectDir), // projects/
		l.ProjectDir,
		l.KnowledgeMD,
		filepath.Join(l.KnowledgeMD, "decisions"),
		l.Worktrees,
		l.Artifacts,
		l.Logs,
	}
}

// Ensure creates the directory layout idempotently. Safe to call repeatedly.
func (l Layout) Ensure() error {
	for _, d := range l.dirs() {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %q: %w", d, err)
		}
	}
	return nil
}

// Writable verifies the engine home exists and is writable, by creating and removing a temp file.
// Reported by `cwv2 doctor` (SSD writable check).
func (l Layout) Writable() error {
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return fmt.Errorf("engine home %q not creatable: %w", l.Root, err)
	}
	f, err := os.CreateTemp(l.Root, ".cwv2-writecheck-*")
	if err != nil {
		return fmt.Errorf("engine home %q not writable: %w", l.Root, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// Missing returns the subset of expected directories that do not yet exist (for doctor reporting).
func (l Layout) Missing() []string {
	var missing []string
	for _, d := range l.dirs() {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			missing = append(missing, d)
		}
	}
	return missing
}
