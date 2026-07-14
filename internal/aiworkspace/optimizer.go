package aiworkspace

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// OptimizerCategory groups optimizers for the UI filter chips.
type OptimizerCategory = string

const (
	CatContent OptimizerCategory = "content"
	CatContext OptimizerCategory = "context"
	CatRepo    OptimizerCategory = "repo"
	CatCache   OptimizerCategory = "cache"
	CatFilter  OptimizerCategory = "filter"
)

// FieldSpec describes one config knob; the UI auto-generates a form from these (no per-optimizer UI).
type FieldSpec struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // bool | int | string | select
	Default any      `json:"default"`
	Options []string `json:"options,omitempty"` // for select
	Help    string   `json:"help,omitempty"`
}

// OptimizerMeta is the self-description every optimizer publishes.
type OptimizerMeta struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Category     OptimizerCategory `json:"category"`
	Version      string            `json:"version"`
	Kinds        []string          `json:"kinds"` // content kinds it accepts (informational)
	ConfigSchema []FieldSpec       `json:"configSchema"`
	// RequiresCompanion marks optimizers whose work (embeddings, vector search) runs in the local
	// companion daemon, not the core. The service routes these to the companion and the UI badges them.
	RequiresCompanion bool `json:"requiresCompanion,omitempty"`
}

// OptimizeInput is the content + config handed to an optimizer.
type OptimizeInput struct {
	Kind    string
	Content []byte
	Config  map[string]any
}

// OptimizeOutput is the optimized content plus measured savings.
type OptimizeOutput struct {
	Content      []byte
	TokensBefore int
	TokensAfter  int
	Notes        []string
	Artifacts    map[string][]byte
}

// Optimizer is THE plugin contract. A new optimizer is one file: implement Meta + Optimize and call
// Register in init(). Stats/health are tracked centrally, so optimizers stay pure and stateless.
type Optimizer interface {
	Meta() OptimizerMeta
	Optimize(ctx context.Context, in OptimizeInput) (OptimizeOutput, error)
}

var (
	regMu    sync.RWMutex
	registry = map[string]Optimizer{}
)

// Register adds an optimizer to the global registry (call from init()). Last registration for an id wins.
func Register(o Optimizer) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[o.Meta().ID] = o
}

// ListOptimizers returns all registered optimizers, sorted by category then name (stable UI order).
func ListOptimizers() []Optimizer {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Optimizer, 0, len(registry))
	for _, o := range registry {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		mi, mj := out[i].Meta(), out[j].Meta()
		if mi.Category != mj.Category {
			return mi.Category < mj.Category
		}
		return mi.Name < mj.Name
	})
	return out
}

// GetOptimizer returns one optimizer by id.
func GetOptimizer(id string) (Optimizer, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	o, ok := registry[id]
	return o, ok
}

// DefaultConfig builds a config map from a meta's schema defaults.
func DefaultConfig(m OptimizerMeta) map[string]any {
	cfg := map[string]any{}
	for _, f := range m.ConfigSchema {
		cfg[f.Key] = f.Default
	}
	return cfg
}

// mergeConfig overlays stored/overridden values on top of the schema defaults.
func mergeConfig(m OptimizerMeta, over map[string]any) map[string]any {
	cfg := DefaultConfig(m)
	for k, v := range over {
		if _, ok := cfg[k]; ok && v != nil {
			cfg[k] = v
		}
	}
	return cfg
}

// --- config accessors (tolerant of JSON's float64 numbers) ---

func cfgBool(c map[string]any, key string, def bool) bool {
	if v, ok := c[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func cfgInt(c map[string]any, key string, def int) int {
	if v, ok := c[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return def
}

func cfgString(c map[string]any, key, def string) string {
	if v, ok := c[key]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return def
}
