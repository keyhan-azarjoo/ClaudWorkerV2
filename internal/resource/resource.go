// Package resource is the Resource Manager (docs/21 S7A).
//
// The Resource Manager owns RESOURCES — the accounts, runtimes, devices, machines, and worktrees the
// engine draws on. Its responsibilities are: discovery, registration, health, availability,
// reservation, release, metrics, and scheduling metadata.
//
// It contains NO ownership logic and NO locking. Reservation here is a TRANSIENT, in-memory
// availability marker for scheduling — it is not a durable lock. Durable resource ownership, TTL,
// crash recovery, and lock persistence belong to the Lock Manager (S7B), which builds on top of this
// inventory. Keeping the two apart is the S7 split.
//
// V1 heritage (simplified, behaviour preserved). The mature V1 concepts — account pacing, cooldowns,
// failover, scheduling, and health monitoring — live here as resource STATE and mechanics (health,
// cooldown windows, usage metrics, deterministic selection/failover). The pause/spend RULE itself is
// NOT duplicated here: that decision stays in the Policy Engine's BudgetPolicy (S6). The Resource
// Manager supplies the state; the Policy Engine decides.
package resource

import (
	"sort"
	"sync"
	"time"
)

// Kind classifies a resource. New kinds are added as the fleet grows (no behaviour change needed).
type Kind string

const (
	KindClaudeAccount Kind = "claude_account"
	KindCodexAccount  Kind = "codex_account"
	KindLocalRuntime  Kind = "local_runtime"
	KindAndroidDevice Kind = "android_device"
	KindIPhone        Kind = "iphone"
	KindESP32         Kind = "esp32"
	KindMacMini       Kind = "mac_mini"
	KindBuildMachine  Kind = "build_machine"
	KindGitWorktree   Kind = "git_worktree"
)

// Health is the last-known operational state of a resource (health monitoring).
type Health string

const (
	HealthUnknown  Health = "unknown"
	HealthHealthy  Health = "healthy"
	HealthDegraded Health = "degraded" // usable but deprioritised
	HealthDown     Health = "down"     // not usable
)

// Availability is the DERIVED, schedulable state (never stored directly — computed from health +
// cooldown + reservation).
type Availability string

const (
	Available AvailabilityValue = "available"
	Reserved  AvailabilityValue = "reserved"
	Cooldown  AvailabilityValue = "cooldown"
	Offline   AvailabilityValue = "offline"
	Paused    AvailabilityValue = "paused" // manually paused by the operator (never auto-selected)
)

// AvailabilityValue is the type of the derived availability constants.
type AvailabilityValue = Availability

// Metrics are the observable counters/timestamps for one resource (scheduling + observability).
type Metrics struct {
	UsagePct     int       `json:"usage_pct"`      // last-known usage (e.g. 5h account usage); input for pacing
	Reservations int       `json:"reservations"`   // times reserved
	Uses         int       `json:"uses"`           // times used to completion
	Failures     int       `json:"failures"`       // recorded failures
	LastUsed     time.Time `json:"last_used"`      // for LRU scheduling
	LastHealthAt time.Time `json:"last_health_at"` // when health was last set
}

// Resource is one managed resource. It carries scheduling metadata (Labels) and the pacing/cooldown
// window. The reservation holder is transient (in-memory only) — durable ownership is S7B.
type Resource struct {
	ID            string            `json:"id"`
	Kind          Kind              `json:"kind"`
	Name          string            `json:"name"`
	Labels        map[string]string `json:"labels,omitempty"` // scheduling metadata (region, caps, ...)
	Health        Health            `json:"health"`
	CooldownUntil time.Time         `json:"cooldown_until,omitempty"` // unavailable until this time
	Metrics       Metrics           `json:"metrics"`

	reservedBy string // transient reservation holder; NOT persisted, NOT a durable lock (S7B)
	paused     bool   // operator pause (manual); paused resources are never selected for work
}

func (r *Resource) clone() *Resource {
	cp := *r
	if r.Labels != nil {
		cp.Labels = make(map[string]string, len(r.Labels))
		for k, v := range r.Labels {
			cp.Labels[k] = v
		}
	}
	return &cp
}

// Manager is the in-memory resource inventory. It is safe for concurrent use. It holds no persistent
// state: on restart the inventory is rebuilt by discovery, and transient reservations are simply gone
// (correct — durable ownership is the Lock Manager's job, S7B).
type Manager struct {
	mu  sync.Mutex
	res map[string]*Resource
	now func() time.Time
}

// Option configures a Manager.
type Option func(*Manager)

// WithClock overrides the time source (tests inject a fixed/steady clock for deterministic scheduling).
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// New returns an empty Manager.
func New(opts ...Option) *Manager {
	m := &Manager{res: map[string]*Resource{}, now: time.Now}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Register adds or replaces a resource (registration). Health defaults to Unknown if unset.
func (m *Manager) Register(r Resource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Health == "" {
		r.Health = HealthUnknown
	}
	cp := r.clone()
	m.res[r.ID] = cp
}

// Deregister removes a resource. Returns false if it was not present.
func (m *Manager) Deregister(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.res[id]; !ok {
		return false
	}
	delete(m.res, id)
	return true
}

// Get returns a copy of one resource.
func (m *Manager) Get(id string) (*Resource, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[id]
	if !ok {
		return nil, false
	}
	return r.clone(), true
}

// Filter narrows a listing/selection. Zero value matches everything.
type Filter struct {
	Kind   Kind              // "" = any kind
	Labels map[string]string // all must match (subset)
}

func (f Filter) matches(r *Resource) bool {
	if f.Kind != "" && r.Kind != f.Kind {
		return false
	}
	for k, v := range f.Labels {
		if r.Labels[k] != v {
			return false
		}
	}
	return true
}

// List returns copies of all resources matching the filter, sorted by ID (deterministic).
func (m *Manager) List(f Filter) []*Resource {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Resource, 0, len(m.res))
	for _, r := range m.res {
		if f.matches(r) {
			out = append(out, r.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
