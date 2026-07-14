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

// The Scan subsystem walks real on-disk folders, finds optimizable files (.md/.json/.yaml/.xml/.log/…),
// and can rewrite them IN PLACE so the user's coding tools (VS Code, Cursor, terminal AI) immediately
// read the leaner versions. Every in-place write is backed up first to a central manifest, so any change
// is reversible via Restore. This is the piece that makes optimization affect the actual computer, not
// just pasted text.

// ScanFile is one discovered file's report line.
type ScanFile struct {
	Path         string `json:"path"` // absolute
	Rel          string `json:"rel"`
	Root         string `json:"root"`
	Type         string `json:"type"` // md|json|yaml|xml|log|txt
	Optimizer    string `json:"optimizer"`
	Bytes        int    `json:"bytes"`
	TokensBefore int    `json:"tokensBefore"`
	TokensAfter  int    `json:"tokensAfter"`
	Saved        int    `json:"saved"`
	Optimizable  bool   `json:"optimizable"` // after < before
	HasBackup    bool   `json:"hasBackup"`
}

// ScanResult is a whole scan's report.
type ScanResult struct {
	Roots       []string   `json:"roots"`
	Files       []ScanFile `json:"files"`
	Count       int        `json:"count"`
	TotalBytes  int        `json:"totalBytes"`
	TotalBefore int        `json:"totalBefore"`
	TotalAfter  int        `json:"totalAfter"`
	TotalSaved  int        `json:"totalSaved"`
	Truncated   bool       `json:"truncated"`
	Notes       []string   `json:"notes,omitempty"`
}

// backupEntry records one in-place optimization so it can be restored.
type backupEntry struct {
	Path      string `json:"path"`
	Backup    string `json:"backup"` // blob filename
	At        string `json:"at"`
	OrigBytes int    `json:"origBytes"`
	OptBytes  int    `json:"optBytes"`
	Optimizer string `json:"optimizer"`
}

const scanFileCap = 4000 // safety cap on files per scan

type scanner struct {
	dir   string // scan-backups dir
	blobs string
	mu    sync.Mutex
}

func newScanner(base string) *scanner {
	d := filepath.Join(base, "scan-backups")
	b := filepath.Join(d, "blobs")
	_ = os.MkdirAll(b, 0o755)
	return &scanner{dir: d, blobs: b}
}

func (s *scanner) manifestPath() string { return filepath.Join(s.dir, "manifest.json") }

func (s *scanner) loadManifest() map[string]backupEntry {
	out := map[string]backupEntry{}
	if b, err := os.ReadFile(s.manifestPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (s *scanner) saveManifest(m map[string]backupEntry) {
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(s.manifestPath(), b, 0o644)
	}
}

// typeGroup maps a file extension to a scan type group ("" = not optimizable).
func typeGroup(ext string) string {
	switch strings.ToLower(ext) {
	case ".md", ".markdown", ".mdx":
		return "md"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".xml", ".html", ".htm":
		return "xml"
	case ".log":
		return "log"
	case ".txt", ".text":
		return "txt"
	}
	return ""
}

// optimizerForType maps a scan type group to the optimizer that handles it.
func optimizerForType(group string) string {
	switch group {
	case "md":
		return "markdown"
	case "json":
		return "json"
	case "yaml":
		return "yaml"
	case "xml":
		return "xml"
	case "log":
		return "log"
	case "txt":
		return "compress"
	}
	return ""
}

// optimizeContent runs the mapped optimizer for a file with its default config.
func optimizeContent(group string, data []byte) (OptimizeOutput, string, bool) {
	optID := optimizerForType(group)
	o, ok := GetOptimizer(optID)
	if !ok {
		return OptimizeOutput{}, "", false
	}
	out, err := o.Optimize(context.Background(), OptimizeInput{Kind: group, Content: data, Config: DefaultConfig(o.Meta())})
	if err != nil {
		return OptimizeOutput{}, optID, false
	}
	return out, optID, true
}

// scan walks the roots and reports every optimizable file (dry-run — no writes). types filters the type
// groups ("" / empty = all).
func (s *scanner) scan(roots []string, types []string) ScanResult {
	allow := map[string]bool{}
	for _, t := range types {
		if t = strings.TrimSpace(t); t != "" {
			allow[t] = true
		}
	}
	manifest := s.loadManifest()

	var res ScanResult
	res.Roots = roots
	seen := map[string]bool{}
	home := filepath.Clean(os.Getenv("HOME"))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			res.Notes = append(res.Notes, "bad path: "+root)
			continue
		}
		if abs == "/" || abs == home {
			// refuse a blind whole-home/root scan (too broad); a subfolder is fine.
			res.Notes = append(res.Notes, "refused (too broad): "+abs+" — pick a subfolder")
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			res.Notes = append(res.Notes, "not a folder: "+abs)
			continue
		}
		_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				if ctxIgnore[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
					return fs.SkipDir
				}
				return nil
			}
			group := typeGroup(filepath.Ext(d.Name()))
			if group == "" {
				return nil
			}
			if len(allow) > 0 && !allow[group] {
				return nil
			}
			if seen[p] {
				return nil
			}
			fi, e := d.Info()
			if e != nil || fi.Size() > maxFileBytes {
				return nil
			}
			data, e := os.ReadFile(p)
			if e != nil {
				return nil
			}
			out, optID, ok := optimizeContent(group, data)
			if !ok {
				return nil
			}
			before := EstimateTokens(string(data))
			after := out.TokensAfter
			seen[p] = true
			res.Files = append(res.Files, ScanFile{
				Path: p, Rel: relOf(abs, p), Root: abs, Type: group, Optimizer: optID,
				Bytes: len(data), TokensBefore: before, TokensAfter: after, Saved: before - after,
				Optimizable: after < before, HasBackup: manifest[HashKey(p)].Path != "",
			})
			res.TotalBytes += len(data)
			res.TotalBefore += before
			res.TotalAfter += after
			if before-after > 0 {
				res.TotalSaved += before - after
			}
			if len(res.Files) >= scanFileCap {
				res.Truncated = true
				return fs.SkipAll
			}
			return nil
		})
		if res.Truncated {
			break
		}
	}
	// Biggest savings first.
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Saved > res.Files[j].Saved })
	res.Count = len(res.Files)
	if res.Truncated {
		res.Notes = append(res.Notes, fmt.Sprintf("stopped at %d files — narrow the folder for the rest", scanFileCap))
	}
	return res
}

func relOf(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

// optimizeFiles rewrites each path in place after backing up the original. Skips files with no savings
// or an empty result (safety). Returns a per-file result.
func (s *scanner) optimizeFiles(paths []string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest := s.loadManifest()
	out := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		res := map[string]any{"path": p, "ok": false}
		group := typeGroup(filepath.Ext(p))
		if group == "" {
			res["error"] = "unsupported type"
			out = append(out, res)
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			res["error"] = "read failed"
			out = append(out, res)
			continue
		}
		oo, optID, ok := optimizeContent(group, data)
		if !ok {
			res["error"] = "optimizer failed"
			out = append(out, res)
			continue
		}
		before := EstimateTokens(string(data))
		if len(oo.Content) == 0 && len(data) > 0 {
			res["error"] = "empty result — skipped"
			out = append(out, res)
			continue
		}
		if oo.TokensAfter >= before {
			res["skipped"] = "no savings"
			res["ok"] = true
			out = append(out, res)
			continue
		}
		// Back up the original FIRST — never overwrite without a good backup.
		key := HashKey(p)
		blob := filepath.Join(s.blobs, key)
		if err := os.WriteFile(blob, data, 0o644); err != nil {
			res["error"] = "backup failed — not modified"
			out = append(out, res)
			continue
		}
		if err := os.WriteFile(p, oo.Content, fileMode(p)); err != nil {
			res["error"] = "write failed"
			out = append(out, res)
			continue
		}
		manifest[key] = backupEntry{Path: p, Backup: key, At: time.Now().UTC().Format(time.RFC3339), OrigBytes: len(data), OptBytes: len(oo.Content), Optimizer: optID}
		res["ok"] = true
		res["saved"] = before - oo.TokensAfter
		out = append(out, res)
	}
	s.saveManifest(manifest)
	return out
}

// fileMode preserves the existing file's mode when overwriting (falls back to 0644).
func fileMode(p string) os.FileMode {
	if fi, err := os.Stat(p); err == nil {
		return fi.Mode().Perm()
	}
	return 0o644
}

// restore copies a backed-up original back over the file and drops the backup.
func (s *scanner) restore(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest := s.loadManifest()
	key := HashKey(path)
	e, ok := manifest[key]
	if !ok {
		return fmt.Errorf("no backup for %s", path)
	}
	data, err := os.ReadFile(filepath.Join(s.blobs, e.Backup))
	if err != nil {
		return fmt.Errorf("backup missing")
	}
	if err := os.WriteFile(path, data, fileMode(path)); err != nil {
		return err
	}
	delete(manifest, key)
	_ = os.Remove(filepath.Join(s.blobs, e.Backup))
	s.saveManifest(manifest)
	return nil
}

// backups lists all restorable originals, newest first.
func (s *scanner) backups() []backupEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadManifest()
	out := make([]backupEntry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

// restoreAll reverts every backed-up file. Returns the count restored.
func (s *scanner) restoreAll() int {
	n := 0
	for _, e := range s.backups() {
		if s.restore(e.Path) == nil {
			n++
		}
	}
	return n
}

// preview returns the before/after content for one file (truncated for display).
func (s *scanner) preview(path string) (map[string]any, error) {
	group := typeGroup(filepath.Ext(path))
	if group == "" {
		return nil, fmt.Errorf("unsupported type")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read failed")
	}
	oo, optID, ok := optimizeContent(group, data)
	if !ok {
		return nil, fmt.Errorf("optimizer failed")
	}
	clipN := func(b []byte) string {
		const capN = 40 << 10
		if len(b) > capN {
			return string(b[:capN]) + "\n… (truncated for preview)"
		}
		return string(b)
	}
	return map[string]any{
		"path": path, "optimizer": optID,
		"before": clipN(data), "after": clipN(oo.Content),
		"tokensBefore": EstimateTokens(string(data)), "tokensAfter": oo.TokensAfter,
	}, nil
}
