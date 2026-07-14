package aiworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The Local Companion is an OPTIONAL desktop daemon (not built here) that does the heavy, at-scale work
// the single-dep Go core can't: repository indexing, local embeddings, a vector DB, an optimizing proxy.
// This file is the COMMS LAYER ONLY — a loopback HTTP+JSON client + a graceful "absent" state. The
// contract the daemon is expected to serve (all on 127.0.0.1):
//
//	GET  /health        → {"ok":true,"version":"..."}
//	GET  /capabilities  → {"capabilities":["index","embed","optimize","proxy","vectordb"]}
//	POST /optimize      → {"id","content","config"} ⇒ {"content":"...","notes":[...]}
//
// When no daemon answers, every companion-gated feature reports "requires local companion" instead of
// failing — the seam that lets that work move off-core later.
type companionConfig struct {
	Enabled      bool     `json:"enabled"`
	URL          string   `json:"url"`
	LastSeenAt   string   `json:"lastSeenAt,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type companion struct {
	path   string
	mu     sync.Mutex
	client *http.Client

	// short-lived status cache so the dashboard's 5s refresh doesn't ping the daemon every time.
	cacheAt time.Time
	cached  map[string]any
	now     func() time.Time
}

func newCompanion(dir string) *companion {
	return &companion{
		path:   filepath.Join(dir, "companion.json"),
		client: &http.Client{Timeout: 3 * time.Second},
		now:    time.Now,
	}
}

func (c *companion) loadCfg() companionConfig {
	var cfg companionConfig
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	return cfg
}

func (c *companion) saveCfg(cfg companionConfig) {
	if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(c.path, b, 0o644)
	}
}

// onlyLoopback guards against pointing the companion at a non-local host (it must be a localhost daemon).
func onlyLoopback(url string) bool {
	u := strings.ToLower(url)
	return strings.HasPrefix(u, "http://127.0.0.1") || strings.HasPrefix(u, "http://localhost") ||
		strings.HasPrefix(u, "http://[::1]") || strings.HasPrefix(u, "https://127.0.0.1") || strings.HasPrefix(u, "https://localhost")
}

// status returns a live view of the companion (cached ~10s). configured=false when no URL is set.
func (c *companion) status() map[string]any {
	c.mu.Lock()
	if c.cached != nil && c.now().Sub(c.cacheAt) < 10*time.Second {
		out := c.cached
		c.mu.Unlock()
		return out
	}
	cfg := c.loadCfg()
	c.mu.Unlock()

	if cfg.URL == "" || !cfg.Enabled {
		return c.setCache(map[string]any{"present": false, "configured": cfg.URL != ""})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	caps, err := c.probe(ctx, cfg.URL)
	if err != nil {
		return c.setCache(map[string]any{"present": false, "configured": true, "url": cfg.URL, "error": err.Error()})
	}
	// Persist the last-seen + capabilities.
	c.mu.Lock()
	cfg.LastSeenAt = c.now().UTC().Format(time.RFC3339)
	cfg.Capabilities = caps
	c.saveCfg(cfg)
	c.mu.Unlock()
	return c.setCache(map[string]any{"present": true, "configured": true, "url": cfg.URL, "capabilities": caps})
}

func (c *companion) setCache(v map[string]any) map[string]any {
	c.mu.Lock()
	c.cached = v
	c.cacheAt = c.now()
	c.mu.Unlock()
	return v
}

func (c *companion) invalidate() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// probe hits /health then /capabilities.
func (c *companion) probe(ctx context.Context, base string) ([]string, error) {
	base = strings.TrimRight(base, "/")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("companion unreachable")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("companion health: HTTP %d", resp.StatusCode)
	}
	creq, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/capabilities", nil)
	cresp, err := c.client.Do(creq)
	if err != nil {
		return nil, nil // healthy but no capabilities endpoint
	}
	defer cresp.Body.Close()
	var cap struct {
		Capabilities []string `json:"capabilities"`
	}
	_ = json.NewDecoder(cresp.Body).Decode(&cap)
	return cap.Capabilities, nil
}

func (c *companion) connect(url string) (map[string]any, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("a companion URL is required")
	}
	if !onlyLoopback(url) {
		return nil, fmt.Errorf("the companion must be a localhost daemon (127.0.0.1/localhost)")
	}
	c.mu.Lock()
	c.saveCfg(companionConfig{Enabled: true, URL: url})
	c.mu.Unlock()
	c.invalidate()
	return c.status(), nil
}

func (c *companion) disconnect() {
	c.mu.Lock()
	c.saveCfg(companionConfig{Enabled: false})
	c.mu.Unlock()
	c.invalidate()
}

func (c *companion) present() bool {
	s := c.status()
	return s["present"] == true
}

// optimize offloads one optimizer run to the companion (POST /optimize). Returns a clear error when no
// companion is connected — the "requires local companion" state.
func (c *companion) optimize(ctx context.Context, id, content string, cfg map[string]any) (string, []string, error) {
	conf := c.loadCfg()
	if conf.URL == "" || !conf.Enabled {
		return "", nil, fmt.Errorf("requires a local companion — connect one in Settings")
	}
	body, _ := json.Marshal(map[string]any{"id": id, "content": content, "config": cfg})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(conf.URL, "/")+"/optimize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("companion unreachable — is it running?")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("companion /optimize: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Content string   `json:"content"`
		Notes   []string `json:"notes"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", nil, fmt.Errorf("companion returned an unreadable response")
	}
	return out.Content, out.Notes, nil
}
