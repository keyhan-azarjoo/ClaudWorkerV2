package aiworkspace

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func setupScanDir(t *testing.T) string {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "# Title\n\n\n\n<!-- a comment that adds tokens -->\ntext here   \n\n\n\nmore text\n\n\n")
	writeFile(t, dir, "data.json", "{\n  \"a\": 1,\n  \"b\": [1, 2,   3]\n}\n")
	writeFile(t, dir, "notes.txt", "line one    \n\n\n\n\nline two\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "should be ignored") // ignored dir + non-target ext
	return dir
}

func TestScanFindsOptimizableFiles(t *testing.T) {
	dir := setupScanDir(t)
	svc := New(t.TempDir())
	r := svc.Scan([]string{dir}, nil)
	if r.Count < 3 {
		t.Fatalf("expected >=3 files (md/json/txt), got %d", r.Count)
	}
	if r.TotalSaved <= 0 {
		t.Fatal("expected some token savings across the files")
	}
	// node_modules must be skipped.
	for _, f := range r.Files {
		if filepath.Base(filepath.Dir(f.Path)) == "pkg" {
			t.Fatal("node_modules should have been skipped")
		}
	}
}

func TestScanTypeFilter(t *testing.T) {
	dir := setupScanDir(t)
	svc := New(t.TempDir())
	r := svc.Scan([]string{dir}, []string{"json"})
	if r.Count != 1 || r.Files[0].Type != "json" {
		t.Fatalf("type filter should return only json, got %d files", r.Count)
	}
}

func TestScanOptimizeInPlaceAndRestore(t *testing.T) {
	dir := setupScanDir(t)
	svc := New(t.TempDir())
	mdPath := filepath.Join(dir, "doc.md")
	original, _ := os.ReadFile(mdPath)

	// Optimize in place.
	res := svc.ScanOptimize([]string{mdPath})
	if len(res) != 1 || res[0]["ok"] != true {
		t.Fatalf("optimize should succeed: %+v", res)
	}
	after, _ := os.ReadFile(mdPath)
	if len(after) >= len(original) {
		t.Fatalf("file should be smaller after optimize: %d -> %d", len(original), len(after))
	}
	// Comment should be gone from the real file.
	if string(after) == string(original) {
		t.Fatal("file content should have changed on disk")
	}

	// Backup recorded + visible in a rescan.
	if len(svc.ScanBackups()) != 1 {
		t.Fatal("expected one backup entry")
	}
	r := svc.Scan([]string{dir}, nil)
	for _, f := range r.Files {
		if f.Path == mdPath && !f.HasBackup {
			t.Fatal("rescan should show HasBackup for the optimized file")
		}
	}

	// Restore brings back the exact original.
	if err := svc.ScanRestore(mdPath); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := os.ReadFile(mdPath)
	if string(restored) != string(original) {
		t.Fatal("restore should reproduce the exact original bytes")
	}
	if len(svc.ScanBackups()) != 0 {
		t.Fatal("backup should be cleared after restore")
	}
}

func TestScanRefusesTooBroad(t *testing.T) {
	svc := New(t.TempDir())
	r := svc.Scan([]string{"/"}, nil)
	if r.Count != 0 {
		t.Fatal("scanning / must be refused")
	}
	if len(r.Notes) == 0 {
		t.Fatal("expected a refusal note")
	}
}

func TestScanWorkspacesBatch(t *testing.T) {
	dir := setupScanDir(t)
	svc := New(t.TempDir())
	w, _ := svc.AddWorkspace("Proj")
	svc.UpdateWorkspace(Workspace{ID: w.ID, Name: "Proj", Folders: []string{dir}})
	r := svc.ScanWorkspaces(nil)
	if r.Count < 3 {
		t.Fatalf("workspace scan should find the folder's files, got %d", r.Count)
	}
}

func TestScanPreview(t *testing.T) {
	dir := setupScanDir(t)
	svc := New(t.TempDir())
	p, err := svc.ScanPreview(filepath.Join(dir, "data.json"))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if p["before"] == p["after"] {
		t.Fatal("preview before/after should differ for pretty JSON")
	}
}
