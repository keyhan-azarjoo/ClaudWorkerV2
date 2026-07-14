package aiworkspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// OptimizerStats accumulates per-optimizer run metrics for the UI (saved tokens, latency, health).
type OptimizerStats struct {
	Runs         int     `json:"runs"`
	TokensBefore int     `json:"tokensBefore"`
	TokensAfter  int     `json:"tokensAfter"`
	SavedTokens  int     `json:"savedTokens"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
	LastRunAt    string  `json:"lastRunAt,omitempty"`
	Health       string  `json:"health"` // ok | degraded | error
	LastError    string  `json:"lastError,omitempty"`
}

// OptimizerState is the persisted enabled/config/stats for one optimizer.
type OptimizerState struct {
	Enabled bool           `json:"enabled"`
	Config  map[string]any `json:"config,omitempty"`
	Stats   OptimizerStats `json:"stats"`
}

// optimizerStore persists optimizers.json (a map id→state). Absent ids default to enabled with schema
// defaults, so newly-registered optimizers appear without a migration.
type optimizerStore struct {
	path string
	mu   sync.Mutex
}

func newOptimizerStore(dir string) *optimizerStore {
	return &optimizerStore{path: filepath.Join(dir, "optimizers.json")}
}

func (s *optimizerStore) loadAll() map[string]OptimizerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]OptimizerState{}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *optimizerStore) saveAll(m map[string]OptimizerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(s.path, b, 0o644)
	}
}

// state returns the stored state for an id, defaulting to enabled if unset.
func (s *optimizerStore) state(id string) (OptimizerState, bool) {
	all := s.loadAll()
	st, ok := all[id]
	return st, ok
}

func (s *optimizerStore) setEnabled(id string, enabled bool) {
	all := s.loadAll()
	st := all[id]
	st.Enabled = enabled
	all[id] = st
	s.saveAll(all)
}

func (s *optimizerStore) setConfig(id string, cfg map[string]any) {
	all := s.loadAll()
	st := all[id]
	st.Config = cfg
	all[id] = st
	s.saveAll(all)
}

// recordRun updates rolling stats after an optimizer runs.
func (s *optimizerStore) recordRun(id string, before, after int, latMs float64, err error) {
	all := s.loadAll()
	st := all[id]
	n := st.Stats.Runs
	st.Stats.Runs = n + 1
	st.Stats.TokensBefore += before
	st.Stats.TokensAfter += after
	if saved := before - after; saved > 0 {
		st.Stats.SavedTokens += saved
	}
	// rolling average latency
	st.Stats.AvgLatencyMs = (st.Stats.AvgLatencyMs*float64(n) + latMs) / float64(n+1)
	st.Stats.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		st.Stats.Health = "error"
		st.Stats.LastError = err.Error()
	} else {
		st.Stats.Health = "ok"
		st.Stats.LastError = ""
	}
	all[id] = st
	s.saveAll(all)
}

// aggregate returns total tokens before/after/saved across all optimizers (for the dashboard ratio).
func (s *optimizerStore) aggregate() (before, after, saved int) {
	for _, st := range s.loadAll() {
		before += st.Stats.TokensBefore
		after += st.Stats.TokensAfter
		saved += st.Stats.SavedTokens
	}
	return
}
