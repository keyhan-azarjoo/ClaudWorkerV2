package aiworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceLifecycleAndDashboard(t *testing.T) {
	svc := New(t.TempDir())
	w1, err := svc.AddWorkspace("Alpha")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !w1.Current {
		t.Fatal("first workspace should be current")
	}
	if svc.Dashboard()["workspace"] != "Alpha" {
		t.Fatalf("dashboard workspace should be Alpha, got %v", svc.Dashboard()["workspace"])
	}
	w2, _ := svc.AddWorkspace("Beta")
	svc.SetCurrentWorkspace(w2.ID)
	if svc.Dashboard()["workspace"] != "Beta" {
		t.Fatal("current workspace should switch to Beta")
	}
	// Update attaches repos + optimizers.
	svc.UpdateWorkspace(Workspace{ID: w1.ID, Name: "Alpha", Repos: []string{"repoA"}, Optimizers: []string{"compress"}})
	for _, w := range svc.WorkspacesList() {
		if w.ID == w1.ID && (len(w.Repos) != 1 || len(w.Optimizers) != 1) {
			t.Fatalf("update did not persist: %+v", w)
		}
	}
	// Removing current promotes another to current.
	svc.RemoveWorkspace(w2.ID)
	if _, ok := svc.workspaces.current(); !ok {
		t.Fatal("a workspace should remain current after removing the current one")
	}
}

func TestContextBuildOptimizesAndPersists(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir)
	// A source file with lots of redundant whitespace + comments to give the optimizers something to cut.
	src := filepath.Join(dir, "doc.md")
	_ = os.WriteFile(src, []byte("# Title\n\n\n\n<!-- comment -->\ntext   \n\n\n\nmore\n\n\n"), 0o644)

	pack, err := svc.ContextBuild(context.Background(), "", "Pack1", []string{src}, "extra inline text\n\n\n", []string{"markdown", "compress"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if pack.TokensAfter >= pack.TokensBefore {
		t.Fatalf("expected optimization to reduce tokens: %d -> %d", pack.TokensBefore, pack.TokensAfter)
	}
	if pack.Files < 1 {
		t.Fatal("expected at least the inline + file sources counted")
	}
	// Retrievable + content stored.
	got, content, ok := svc.ContextGet(pack.ID)
	if !ok || content == "" || got.Name != "Pack1" {
		t.Fatalf("pack not retrievable: ok=%v name=%q", ok, got.Name)
	}
	// Rebuild reuses the id.
	rb, _ := svc.ContextBuild(context.Background(), pack.ID, "Pack1", []string{src}, "", []string{"compress"})
	if rb.ID != pack.ID {
		t.Fatal("rebuild should reuse the pack id")
	}
	// Pin survives, remove deletes.
	svc.ContextPin(pack.ID, true)
	svc.ContextRemove(pack.ID)
	if _, _, ok := svc.ContextGet(pack.ID); ok {
		t.Fatal("pack should be gone after remove")
	}
}

func TestKnowledgeCRUDAndSearch(t *testing.T) {
	svc := New(t.TempDir())
	it, err := svc.KnowledgeAdd("Deploy runbook", "markdown", "ops", []string{"deploy", "runbook"}, "restart the backend then verify")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	svc.KnowledgeAdd("Random note", "note", "", []string{"misc"}, "nothing to see")

	// Search by tag.
	if hits := svc.KnowledgeList("runbook"); len(hits) != 1 || hits[0].ID != it.ID {
		t.Fatalf("tag search failed: %+v", hits)
	}
	// Search by content.
	if hits := svc.KnowledgeList("verify"); len(hits) != 1 {
		t.Fatalf("content search failed: %d hits", len(hits))
	}
	// Collections.
	cols := svc.KnowledgeCollections()
	if len(cols) != 1 || cols[0] != "ops" {
		t.Fatalf("collections wrong: %v", cols)
	}
	// Update content + retrieve.
	svc.KnowledgeUpdate(it.ID, "Deploy runbook v2", "markdown", "ops", []string{"deploy"}, "new content here", true)
	_, content, ok := svc.KnowledgeGet(it.ID)
	if !ok || !strings.Contains(content, "new content") {
		t.Fatalf("update content failed: ok=%v content=%q", ok, content)
	}
	// Remove.
	svc.KnowledgeRemove(it.ID)
	if hits := svc.KnowledgeList("Deploy"); len(hits) != 0 {
		t.Fatalf("item should be removed, got %d", len(hits))
	}
}
