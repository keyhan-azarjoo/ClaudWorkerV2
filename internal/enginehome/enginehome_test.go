package enginehome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForLayout(t *testing.T) {
	l := For("/home", "proj")
	if l.ProjectDir != filepath.Join("/home", "projects", "proj") {
		t.Errorf("ProjectDir = %q", l.ProjectDir)
	}
	if !strings.HasSuffix(l.KnowledgeDB, filepath.Join("proj", "knowledge.db")) {
		t.Errorf("KnowledgeDB = %q", l.KnowledgeDB)
	}
	if !strings.HasSuffix(l.StateDB, filepath.Join("proj", "state.db")) {
		t.Errorf("StateDB = %q", l.StateDB)
	}
}

func TestEnsureCreatesLayoutIdempotently(t *testing.T) {
	root := t.TempDir()
	l := For(root, "p")

	if missing := l.Missing(); len(missing) == 0 {
		t.Fatal("expected missing dirs before Ensure")
	}
	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if missing := l.Missing(); len(missing) != 0 {
		t.Errorf("after Ensure, still missing: %v", missing)
	}
	// idempotent
	if err := l.Ensure(); err != nil {
		t.Errorf("second Ensure: %v", err)
	}
	for _, d := range []string{l.KnowledgeMD, l.Worktrees, l.Artifacts, l.Logs} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("expected dir %q to exist", d)
		}
	}
}

func TestWritable(t *testing.T) {
	root := t.TempDir()
	l := For(root, "p")
	if err := l.Writable(); err != nil {
		t.Errorf("Writable on temp dir: %v", err)
	}
}
