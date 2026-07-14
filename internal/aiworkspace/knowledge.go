package aiworkspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// KnowledgeItem is one searchable note/doc in the AI Workspace knowledge base (separate from the engine's
// task-knowledge). Content is stored as a blob; the index holds metadata + tags/collection for search.
type KnowledgeItem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"` // note | markdown | doc
	Collection string   `json:"collection,omitempty"`
	Tags       []string `json:"tags"`
	Bytes      int      `json:"bytes"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

type knowledgeStore struct {
	dir   string
	blobs string
	mu    sync.Mutex
}

func newKnowledgeStore(base string) *knowledgeStore {
	d := filepath.Join(base, "knowledge")
	b := filepath.Join(d, "blobs")
	_ = os.MkdirAll(b, 0o755)
	return &knowledgeStore{dir: d, blobs: b}
}

func (k *knowledgeStore) indexPath() string         { return filepath.Join(k.dir, "index.json") }
func (k *knowledgeStore) blobPath(id string) string { return filepath.Join(k.blobs, id) }

func (k *knowledgeStore) loadIndex() map[string]KnowledgeItem {
	out := map[string]KnowledgeItem{}
	if b, err := os.ReadFile(k.indexPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (k *knowledgeStore) saveIndex(m map[string]KnowledgeItem) {
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(k.indexPath(), b, 0o644)
	}
}

func parseTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func (k *knowledgeStore) add(title, kind, collection string, tags []string, content string) (KnowledgeItem, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return KnowledgeItem{}, fmt.Errorf("title is required")
	}
	if kind == "" {
		kind = "note"
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	id := nextID("kn")
	if err := os.WriteFile(k.blobPath(id), []byte(content), 0o644); err != nil {
		return KnowledgeItem{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item := KnowledgeItem{ID: id, Title: title, Kind: kind, Collection: strings.TrimSpace(collection), Tags: parseTags(tags), Bytes: len(content), CreatedAt: now, UpdatedAt: now}
	idx := k.loadIndex()
	idx[id] = item
	k.saveIndex(idx)
	return item, nil
}

func (k *knowledgeStore) update(id, title, kind, collection string, tags []string, content string, contentSet bool) (KnowledgeItem, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	idx := k.loadIndex()
	item, ok := idx[id]
	if !ok {
		return KnowledgeItem{}, fmt.Errorf("unknown item %q", id)
	}
	if strings.TrimSpace(title) != "" {
		item.Title = strings.TrimSpace(title)
	}
	if kind != "" {
		item.Kind = kind
	}
	item.Collection = strings.TrimSpace(collection)
	item.Tags = parseTags(tags)
	if contentSet {
		if err := os.WriteFile(k.blobPath(id), []byte(content), 0o644); err != nil {
			return KnowledgeItem{}, err
		}
		item.Bytes = len(content)
	}
	item.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	idx[id] = item
	k.saveIndex(idx)
	return item, nil
}

func (k *knowledgeStore) remove(id string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	idx := k.loadIndex()
	delete(idx, id)
	_ = os.Remove(k.blobPath(id))
	k.saveIndex(idx)
}

func (k *knowledgeStore) get(id string) (KnowledgeItem, string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	idx := k.loadIndex()
	item, ok := idx[id]
	if !ok {
		return KnowledgeItem{}, "", false
	}
	data, _ := os.ReadFile(k.blobPath(id))
	return item, string(data), true
}

// list returns items (optionally filtered by a search query over title/tags/collection/content), newest
// updated first.
func (k *knowledgeStore) list(query string) []KnowledgeItem {
	k.mu.Lock()
	defer k.mu.Unlock()
	idx := k.loadIndex()
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]KnowledgeItem, 0, len(idx))
	for id, item := range idx {
		if q != "" && !k.matches(id, item, q) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (k *knowledgeStore) matches(id string, item KnowledgeItem, q string) bool {
	if strings.Contains(strings.ToLower(item.Title), q) || strings.Contains(strings.ToLower(item.Collection), q) {
		return true
	}
	for _, t := range item.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	// Fall back to content search (blob read only when metadata misses).
	if data, err := os.ReadFile(k.blobPath(id)); err == nil {
		return strings.Contains(strings.ToLower(string(data)), q)
	}
	return false
}

func (k *knowledgeStore) collections() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, item := range k.loadIndex() {
		if item.Collection != "" && !seen[item.Collection] {
			seen[item.Collection] = true
			out = append(out, item.Collection)
		}
	}
	sort.Strings(out)
	return out
}
