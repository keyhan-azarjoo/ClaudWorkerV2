package aiworkspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// CacheKind groups cache entries. Phase 3 producer is the optimizer cache; the rest (prompt/response/
// embedding/context/…) are ready for the proxy + companion phases.
type CacheKind = string

// CacheEntry is one cached item's metadata (the value is stored as a separate blob).
type CacheEntry struct {
	Key       string    `json:"key"` // sha256 hex
	Kind      CacheKind `json:"kind"`
	Label     string    `json:"label"`
	Bytes     int       `json:"bytes"`
	Tokens    int       `json:"tokens"`
	Hits      int       `json:"hits"`
	Pinned    bool      `json:"pinned"`
	CreatedAt string    `json:"createdAt"`
	ExpiresAt string    `json:"expiresAt,omitempty"`
}

type cacheCounters struct {
	Hits   int `json:"hits"`
	Misses int `json:"misses"`
}

// cacheStore is a content-addressed cache: metadata in index.json, values in blobs/<key>. Lifetime
// hit/miss counters persist in stats.json so the hit ratio survives eviction. Local, dependency-free.
type cacheStore struct {
	dir   string
	blobs string
	mu    sync.Mutex
	now   func() time.Time
}

func newCacheStore(base string) *cacheStore {
	d := filepath.Join(base, "cache")
	b := filepath.Join(d, "blobs")
	_ = os.MkdirAll(b, 0o755)
	return &cacheStore{dir: d, blobs: b, now: time.Now}
}

// HashKey derives a stable content-addressed key from any set of parts.
func HashKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *cacheStore) indexPath() string          { return filepath.Join(c.dir, "index.json") }
func (c *cacheStore) statsPath() string          { return filepath.Join(c.dir, "stats.json") }
func (c *cacheStore) blobPath(key string) string { return filepath.Join(c.blobs, key) }

func (c *cacheStore) loadIndex() map[string]CacheEntry {
	out := map[string]CacheEntry{}
	if b, err := os.ReadFile(c.indexPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (c *cacheStore) saveIndex(m map[string]CacheEntry) {
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(c.indexPath(), b, 0o644)
	}
}

func (c *cacheStore) loadCounters() cacheCounters {
	var cc cacheCounters
	if b, err := os.ReadFile(c.statsPath()); err == nil {
		_ = json.Unmarshal(b, &cc)
	}
	return cc
}

func (c *cacheStore) saveCounters(cc cacheCounters) {
	if b, err := json.Marshal(cc); err == nil {
		_ = os.WriteFile(c.statsPath(), b, 0o644)
	}
}

// expired reports whether an entry has an ExpiresAt in the past.
func (c *cacheStore) expired(e CacheEntry) bool {
	if e.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, e.ExpiresAt)
	return err == nil && c.now().After(t)
}

// Get returns a cached value and true on hit (bumping the entry + lifetime hit counter); on miss it
// bumps the miss counter and returns false. Expired entries are treated as misses and removed.
func (c *cacheStore) Get(key string) (CacheEntry, []byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	cc := c.loadCounters()
	e, ok := idx[key]
	if !ok || c.expired(e) {
		if ok { // expired → evict
			delete(idx, key)
			_ = os.Remove(c.blobPath(key))
			c.saveIndex(idx)
		}
		cc.Misses++
		c.saveCounters(cc)
		return CacheEntry{}, nil, false
	}
	val, err := os.ReadFile(c.blobPath(key))
	if err != nil { // blob missing → treat as miss
		delete(idx, key)
		c.saveIndex(idx)
		cc.Misses++
		c.saveCounters(cc)
		return CacheEntry{}, nil, false
	}
	e.Hits++
	idx[key] = e
	c.saveIndex(idx)
	cc.Hits++
	c.saveCounters(cc)
	return e, val, true
}

// Put stores/overwrites a value.
func (c *cacheStore) Put(kind, key, label string, value []byte, tokens int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.WriteFile(c.blobPath(key), value, 0o644); err != nil {
		return
	}
	idx := c.loadIndex()
	prev := idx[key]
	idx[key] = CacheEntry{
		Key: key, Kind: kind, Label: label, Bytes: len(value), Tokens: tokens,
		Hits: prev.Hits, Pinned: prev.Pinned, CreatedAt: c.now().UTC().Format(time.RFC3339),
	}
	c.saveIndex(idx)
}

// List returns entries newest-first, capped.
func (c *cacheStore) List(limit int) []CacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	out := make([]CacheEntry, 0, len(idx))
	for _, e := range idx {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Clear removes all entries (kind=="") or one kind. Pinned entries are kept. Returns removed count.
func (c *cacheStore) Clear(kind string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	removed := 0
	for k, e := range idx {
		if e.Pinned {
			continue
		}
		if kind == "" || e.Kind == kind {
			delete(idx, k)
			_ = os.Remove(c.blobPath(k))
			removed++
		}
	}
	c.saveIndex(idx)
	return removed
}

func (c *cacheStore) Pin(key string, pinned bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	if e, ok := idx[key]; ok {
		e.Pinned = pinned
		idx[key] = e
		c.saveIndex(idx)
	}
}

func (c *cacheStore) Expire(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	delete(idx, key)
	_ = os.Remove(c.blobPath(key))
	c.saveIndex(idx)
}

// Stats returns the hit ratio, per-kind rollups and estimated saved tokens (tokens×hits).
func (c *cacheStore) Stats() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.loadIndex()
	cc := c.loadCounters()
	type kindRoll struct {
		Kind   string `json:"kind"`
		Count  int    `json:"count"`
		Bytes  int    `json:"bytes"`
		Tokens int    `json:"tokens"`
		Hits   int    `json:"hits"`
	}
	byKind := map[string]*kindRoll{}
	saved, totalBytes := 0, 0
	for _, e := range idx {
		r := byKind[e.Kind]
		if r == nil {
			r = &kindRoll{Kind: e.Kind}
			byKind[e.Kind] = r
		}
		r.Count++
		r.Bytes += e.Bytes
		r.Tokens += e.Tokens
		r.Hits += e.Hits
		saved += e.Tokens * e.Hits
		totalBytes += e.Bytes
	}
	kinds := make([]*kindRoll, 0, len(byKind))
	for _, r := range byKind {
		kinds = append(kinds, r)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	ratio := 0
	if tot := cc.Hits + cc.Misses; tot > 0 {
		ratio = int(float64(cc.Hits) / float64(tot) * 100)
	}
	return map[string]any{
		"entries":     len(idx),
		"bytes":       totalBytes,
		"hits":        cc.Hits,
		"misses":      cc.Misses,
		"hitRatio":    ratio,
		"savedTokens": saved,
		"byKind":      kinds,
	}
}

// hitRatio is a convenience for the dashboard.
func (c *cacheStore) hitRatio() int {
	cc := c.loadCounters()
	if tot := cc.Hits + cc.Misses; tot > 0 {
		return int(float64(cc.Hits) / float64(tot) * 100)
	}
	return 0
}
