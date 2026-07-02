package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Capabilities is what the Assignment Engine REQUESTS. It describes needs, not a provider — the
// engine never names Claude. Extend as providers gain features.
type Capabilities struct {
	Vision      bool   // needs image understanding
	LongContext bool   // needs a very large context window
	Preferred   string // optional explicit provider preference (still validated against availability)
}

// RuntimeConfig lists available runtimes, a preference order, and per-runtime capability support.
type RuntimeConfig struct {
	Available []string            // registered runtime names, e.g. ["claude"]
	Order     []string            // preference order among Available
	Default   string              // fallback when nothing better matches
	Supports  map[string][]string // runtime -> capability tokens it supports ("vision","long-context")
}

// RuntimeSelectionPolicy chooses the runtime deterministically from requested capabilities. The
// Assignment Engine never chooses a provider directly (S6).
type RuntimeSelectionPolicy struct{ cfg RuntimeConfig }

// RuntimeDecision is the observable answer.
type RuntimeDecision struct {
	Runtime string
	Reason  string
}

// Select returns the runtime that satisfies caps, honouring an explicit preference first, then the
// configured order, then the default. Deterministic: identical config + caps → identical choice.
func (p RuntimeSelectionPolicy) Select(caps Capabilities) RuntimeDecision {
	need := requiredCaps(caps)
	avail := toSet(p.cfg.Available)

	// 1. explicit preference, if available and capable
	if caps.Preferred != "" {
		if avail[caps.Preferred] && p.supports(caps.Preferred, need) {
			return RuntimeDecision{Runtime: caps.Preferred, Reason: "preferred runtime satisfies capabilities"}
		}
	}
	// 2. first in preference order that is available and capable
	for _, name := range p.order() {
		if avail[name] && p.supports(name, need) {
			return RuntimeDecision{Runtime: name, Reason: fmt.Sprintf("first ordered runtime satisfying %v", need)}
		}
	}
	// 3. default (only if it exists among Available)
	if p.cfg.Default != "" && avail[p.cfg.Default] {
		return RuntimeDecision{Runtime: p.cfg.Default, Reason: "fell back to default runtime"}
	}
	return RuntimeDecision{Runtime: "", Reason: fmt.Sprintf("no available runtime satisfies %v", need)}
}

// order returns the configured order, or Available sorted for determinism if no order is set.
func (p RuntimeSelectionPolicy) order() []string {
	if len(p.cfg.Order) > 0 {
		return p.cfg.Order
	}
	out := append([]string(nil), p.cfg.Available...)
	sort.Strings(out)
	return out
}

func (p RuntimeSelectionPolicy) supports(runtime string, need []string) bool {
	if len(need) == 0 {
		return true
	}
	have := toSet(p.cfg.Supports[runtime])
	for _, c := range need {
		if !have[c] {
			return false
		}
	}
	return true
}

func requiredCaps(c Capabilities) []string {
	var need []string
	if c.Vision {
		need = append(need, "vision")
	}
	if c.LongContext {
		need = append(need, "long-context")
	}
	return need
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[strings.TrimSpace(i)] = true
	}
	return m
}
