package aiworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ContextPack is a reusable, pre-optimized bundle of source material (files/folders or pasted text) with
// a chain of optimizers applied. Pinned packs are kept across clears; the assembled content is stored as
// a blob so it can be viewed/reused.
type ContextPack struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Sources      []string `json:"sources"`
	Optimizers   []string `json:"optimizers"`
	TokensBefore int      `json:"tokensBefore"`
	TokensAfter  int      `json:"tokensAfter"`
	Bytes        int      `json:"bytes"`
	Files        int      `json:"files"`
	Pinned       bool     `json:"pinned"`
	Notes        []string `json:"notes,omitempty"`
	CreatedAt    string   `json:"createdAt"`
}

// context read caps: keep assembly bounded so a stray huge tree can't blow up memory.
const (
	maxFileBytes  = 512 << 10 // 512 KB per file
	maxTotalBytes = 4 << 20   // 4 MB assembled
)

var ctxIgnore = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true, ".next": true,
	"vendor": true, "__pycache__": true, ".venv": true, "coverage": true,
}

type contextStore struct {
	dir   string
	blobs string
	mu    sync.Mutex
}

func newContextStore(base string) *contextStore {
	d := filepath.Join(base, "context")
	b := filepath.Join(d, "blobs")
	_ = os.MkdirAll(b, 0o755)
	return &contextStore{dir: d, blobs: b}
}

func (c *contextStore) indexPath() string         { return filepath.Join(c.dir, "index.json") }
func (c *contextStore) blobPath(id string) string { return filepath.Join(c.blobs, id) }

func (c *contextStore) loadIndex() map[string]ContextPack {
	out := map[string]ContextPack{}
	if b, err := os.ReadFile(c.indexPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (c *contextStore) saveIndex(m map[string]ContextPack) {
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(c.indexPath(), b, 0o644)
	}
}

// assemble reads the given sources (files or dirs) plus optional inline text into one document with
// per-file headers, honoring the per-file/total caps and the ignore set.
func assembleSources(sources []string, inline string) (string, int, []string) {
	var b strings.Builder
	total := 0
	files := 0
	notes := []string{}
	add := func(label, content string) bool {
		if total+len(content) > maxTotalBytes {
			notes = append(notes, "reached total size cap")
			return false
		}
		if label != "" {
			b.WriteString("\n// ==== " + label + " ====\n")
		}
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteByte('\n')
		}
		total += len(content)
		files++
		return true
	}
	if strings.TrimSpace(inline) != "" {
		add("inline", inline)
	}
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		info, err := os.Stat(src)
		if err != nil {
			notes = append(notes, "skipped (not found): "+src)
			continue
		}
		if info.IsDir() {
			_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if ctxIgnore[d.Name()] {
						return fs.SkipDir
					}
					return nil
				}
				if fi, e := d.Info(); e == nil && fi.Size() > maxFileBytes {
					return nil
				}
				data, e := os.ReadFile(p)
				if e != nil {
					return nil
				}
				if !add(p, string(data)) {
					return fs.SkipAll
				}
				return nil
			})
			continue
		}
		if info.Size() > maxFileBytes {
			notes = append(notes, "skipped (too large): "+src)
			continue
		}
		data, e := os.ReadFile(src)
		if e != nil {
			continue
		}
		add(src, string(data))
	}
	return b.String(), files, notes
}

// applyOptimizers runs a chain of optimizers over content in order, collecting their notes.
func applyOptimizers(ctx context.Context, content string, optimizerIDs []string) (string, []string) {
	notes := []string{}
	for _, id := range optimizerIDs {
		o, ok := GetOptimizer(id)
		if !ok {
			continue
		}
		out, err := o.Optimize(ctx, OptimizeInput{Kind: "text", Content: []byte(content), Config: DefaultConfig(o.Meta())})
		if err != nil {
			notes = append(notes, id+": "+err.Error())
			continue
		}
		content = string(out.Content)
		notes = append(notes, o.Meta().Name+" → "+fmt.Sprintf("%d tok", EstimateTokens(content)))
	}
	return content, notes
}

// build assembles + optimizes sources into a stored pack. Reuses an existing pack's id when rebuilding.
func (c *contextStore) build(ctx context.Context, id, name string, sources []string, inline string, optimizerIDs []string) (ContextPack, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ContextPack{}, fmt.Errorf("pack name is required")
	}
	raw, files, aNotes := assembleSources(sources, inline)
	before := EstimateTokens(raw)
	optimized, oNotes := applyOptimizers(ctx, raw, optimizerIDs)
	after := EstimateTokens(optimized)

	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	if id == "" {
		id = nextID("ctx")
	}
	if err := os.WriteFile(c.blobPath(id), []byte(optimized), 0o644); err != nil {
		return ContextPack{}, err
	}
	prev := idx[id]
	pack := ContextPack{
		ID: id, Name: name, Sources: sources, Optimizers: optimizerIDs,
		TokensBefore: before, TokensAfter: after, Bytes: len(optimized), Files: files,
		Pinned: prev.Pinned, Notes: append(aNotes, oNotes...), CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if pack.Sources == nil {
		pack.Sources = []string{}
	}
	idx[id] = pack
	c.saveIndex(idx)
	return pack, nil
}

func (c *contextStore) list() []ContextPack {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	out := make([]ContextPack, 0, len(idx))
	for _, p := range idx {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (c *contextStore) get(id string) (ContextPack, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	p, ok := idx[id]
	if !ok {
		return ContextPack{}, "", false
	}
	data, _ := os.ReadFile(c.blobPath(id))
	return p, string(data), true
}

func (c *contextStore) pin(id string, pinned bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	if p, ok := idx[id]; ok {
		p.Pinned = pinned
		idx[id] = p
		c.saveIndex(idx)
	}
}

func (c *contextStore) remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	delete(idx, id)
	_ = os.Remove(c.blobPath(id))
	c.saveIndex(idx)
}
