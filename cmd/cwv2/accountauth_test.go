package main

import (
	"os"
	"testing"

	"claudworker/internal/config"
)

func TestAccountAuthHelpers(t *testing.T) {
	accts := []config.Account{
		{Name: "MyOTGO", ConfigDir: "/abs/myotgo"},
		{Name: "Codey", ConfigDir: "~/.cw-accounts/codey", Engine: "codex"},
	}
	a := newAccountAuth(accts, "")
	if a.claudeBin != "claude" || a.codexBin != "codex" {
		t.Fatalf("default bins wrong: %q %q", a.claudeBin, a.codexBin)
	}

	if _, ok := a.find("MyOTGO"); !ok {
		t.Fatal("find should locate MyOTGO")
	}
	if _, ok := a.find("nope"); ok {
		t.Fatal("find should miss unknown account")
	}

	if engineOf(accts[0]) != "claude" {
		t.Fatal("default engine should be claude")
	}
	if engineOf(accts[1]) != "codex" {
		t.Fatal("codex engine not detected")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := expandHome("~/foo"); got != home+"/foo" {
		t.Fatalf("~ not expanded: %q", got)
	}
	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("absolute path should be unchanged: %q", got)
	}
}

func TestFirstLineS(t *testing.T) {
	if firstLineS("a\nb\nc") != "a" {
		t.Fatal("firstLineS should return the first line")
	}
	if firstLineS("solo") != "solo" {
		t.Fatal("firstLineS single line")
	}
}

func TestBeginLoginUnknownAccount(t *testing.T) {
	a := newAccountAuth(nil, "")
	if _, err := a.beginLogin("ghost"); err == nil {
		t.Fatal("beginLogin should error for an unknown account")
	}
	if _, err := a.submitCode("ghost", "x"); err == nil {
		t.Fatal("submitCode should error when no login is pending")
	}
}
