package main

import (
	"testing"
	"time"
)

func TestGrantStoreAddRemove(t *testing.T) {
	dir := t.TempDir()
	gs := newGrantStore(dir)

	if _, err := gs.add("/definitely/not/a/real/path", "always"); err == nil {
		t.Fatal("adding a nonexistent path should error")
	}
	if _, err := gs.add(dir, "always"); err != nil {
		t.Fatalf("add existing dir: %v", err)
	}
	if got := gs.activePaths(); len(got) != 1 {
		t.Fatalf("expected 1 active grant, got %d", len(got))
	}
	// Re-adding the same path updates its scope (no duplicate).
	gs.add(dir, "once")
	g := gs.load()
	if len(g) != 1 || g[0].Scope != "once" {
		t.Fatalf("expected single grant updated to once: %+v", g)
	}
	gs.remove(g[0].Path)
	if len(gs.activePaths()) != 0 {
		t.Fatal("expected no grants after remove")
	}
}

func TestOnceGrantExpiry(t *testing.T) {
	dir := t.TempDir()
	gs := newGrantStore(dir)

	// A stale "once" grant is dropped on load.
	gs.save([]grant{{Path: dir, Scope: "once", CreatedAt: time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339)}})
	if len(gs.load()) != 0 {
		t.Fatal("stale once-grant should expire")
	}
	// A fresh "once" grant survives.
	gs.save([]grant{{Path: dir, Scope: "once", CreatedAt: time.Now().UTC().Format(time.RFC3339)}})
	if len(gs.load()) != 1 {
		t.Fatal("fresh once-grant should remain")
	}
	// An "always" grant never expires, however old.
	gs.save([]grant{{Path: dir, Scope: "always", CreatedAt: time.Now().Add(-10000 * time.Hour).UTC().Format(time.RFC3339)}})
	if len(gs.load()) != 1 {
		t.Fatal("always-grant should never expire")
	}
}
