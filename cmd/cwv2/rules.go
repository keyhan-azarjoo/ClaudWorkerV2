package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ruleEntry is one standing rule every agent must obey BEFORE making a change. Active rules are injected
// into the worker prompt (so the main agent reads them first) and are shown/edited on the Rules page.
type ruleEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Active bool   `json:"active"`
}

// ruleStore is the per-project, persistent registry of rules (own file under the engine home — not
// shared). Seeded once with the owner-mandated defaults.
type ruleStore struct {
	path string
	mu   sync.Mutex
}

func newRuleStore(projectDir string) *ruleStore {
	rs := &ruleStore{path: projectDir + "/rules-registry.json"}
	if _, err := os.Stat(rs.path); err != nil {
		rs.save(defaultRules())
	}
	return rs
}

// defaultRules seeds the store — including the cross-platform UI parity rule the owner asked for.
func defaultRules() []ruleEntry {
	return []ruleEntry{
		{
			ID:     "ui-platform-parity",
			Title:  "UI / platform parity — change EVERY platform, miss nothing",
			Text:   "When you change the app, the website, or ANY user interface, apply the SAME change across ALL platforms and OSes together: the mobile app (iOS, Android) and desktop app (macOS, Windows, Linux), the website, and the hub UI (if it has or needs it). A frontend/UI change must land on every platform that has that surface — never change one and miss the others. Treat 'changed on one platform but not the others' as a defect and fix it in the same task. Before finishing, list the platforms the change affects and confirm each was updated (or explicitly note why a platform legitimately doesn't have that surface).",
			Active: true,
		},
		{
			ID:     "verify-before-done",
			Title:  "Verify on every affected surface before finishing",
			Text:   "Do not mark a task done until you have verified the change actually works on each affected platform/surface (build it, and where a device/simulator is available, exercise it). Never claim a platform is updated without checking it.",
			Active: true,
		},
	}
}

func (rs *ruleStore) load() []ruleEntry {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	b, err := os.ReadFile(rs.path)
	if err != nil {
		return nil
	}
	var out []ruleEntry
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func (rs *ruleStore) save(entries []ruleEntry) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if b, err := json.MarshalIndent(entries, "", "  "); err == nil {
		_ = os.WriteFile(rs.path, b, 0o644)
	}
}

// activeTexts returns the active rules rendered for prompt injection ("Title: Text").
func (rs *ruleStore) activeTexts() []string {
	var out []string
	for _, r := range rs.load() {
		if r.Active {
			t := strings.TrimSpace(r.Title)
			if t != "" && strings.TrimSpace(r.Text) != "" {
				out = append(out, t+": "+strings.TrimSpace(r.Text))
			} else if strings.TrimSpace(r.Text) != "" {
				out = append(out, strings.TrimSpace(r.Text))
			}
		}
	}
	return out
}

func (rs *ruleStore) add(title, text string) ([]ruleEntry, error) {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	if title == "" && text == "" {
		return nil, fmt.Errorf("a rule needs a title or text")
	}
	entries := rs.load()
	id := uniqueRuleID(slugify(title), entries)
	entries = append(entries, ruleEntry{ID: id, Title: title, Text: text, Active: true})
	rs.save(entries)
	return entries, nil
}

func (rs *ruleStore) update(id, title, text string, active bool) []ruleEntry {
	entries := rs.load()
	for i := range entries {
		if entries[i].ID == id {
			entries[i].Title = strings.TrimSpace(title)
			entries[i].Text = strings.TrimSpace(text)
			entries[i].Active = active
		}
	}
	rs.save(entries)
	return entries
}

// setActive toggles a rule without editing its text.
func (rs *ruleStore) setActive(id string, active bool) []ruleEntry {
	entries := rs.load()
	for i := range entries {
		if entries[i].ID == id {
			entries[i].Active = active
		}
	}
	rs.save(entries)
	return entries
}

func (rs *ruleStore) remove(id string) []ruleEntry {
	var out []ruleEntry
	for _, e := range rs.load() {
		if e.ID != id {
			out = append(out, e)
		}
	}
	rs.save(out)
	return out
}

func uniqueRuleID(base string, entries []ruleEntry) string {
	if base == "" {
		base = "rule"
	}
	id := base
	for n := 2; ; n++ {
		taken := false
		for _, e := range entries {
			if e.ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}
