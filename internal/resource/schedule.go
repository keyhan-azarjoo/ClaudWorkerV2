package resource

import (
	"sort"
	"time"
)

// availabilityOf derives the schedulable state (caller must hold the lock). Precedence: a down
// resource is Offline; a cooling resource is Cooldown; a reserved one is Reserved; otherwise
// Available. (Degraded health is still Available — just deprioritised by selection.)
func (m *Manager) availabilityOf(r *Resource) Availability {
	switch {
	case r.paused:
		return Paused
	case r.Health == HealthDown:
		return Offline
	case !r.CooldownUntil.IsZero() && m.now().Before(r.CooldownUntil):
		return Cooldown
	case r.reservedBy != "":
		return Reserved
	default:
		return Available
	}
}

// AvailabilityOf returns the derived availability for one resource.
func (m *Manager) AvailabilityOf(id string) (Availability, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[id]
	if !ok {
		return "", false
	}
	return m.availabilityOf(r), true
}

// Cooldown pauses a resource until `until` (account pacing / rate-limit backoff). The pause DECISION
// belongs to the Policy Engine's BudgetPolicy (S6); this only records the window.
func (m *Manager) Cooldown(id string, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.res[id]; ok {
		r.CooldownUntil = until
	}
}

// SetPaused manually pauses/resumes a resource (operator control, like V1's per-account pause). A
// paused resource is never selected for work until resumed. Returns false if the id is unknown.
func (m *Manager) SetPaused(id string, paused bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[id]
	if ok {
		r.paused = paused
	}
	return ok
}

// IsPaused reports whether a resource is manually paused.
func (m *Manager) IsPaused(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[id]
	return ok && r.paused
}

// candidates returns available resources matching the filter, ordered by the deterministic scheduling
// preference (caller must hold the lock):
//  1. healthier first (Healthy before Degraded);
//  2. lower usage first (pacing — spread load, avoid near-guard accounts);
//  3. least-recently-used first (fair rotation);
//  4. ID ascending (total, deterministic tiebreak).
func (m *Manager) candidates(f Filter) []*Resource {
	var out []*Resource
	for _, r := range m.res {
		if f.matches(r) && m.availabilityOf(r) == Available {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if hr := healthRank(a.Health) - healthRank(b.Health); hr != 0 {
			return hr < 0
		}
		if a.Metrics.UsagePct != b.Metrics.UsagePct {
			return a.Metrics.UsagePct < b.Metrics.UsagePct
		}
		if !a.Metrics.LastUsed.Equal(b.Metrics.LastUsed) {
			return a.Metrics.LastUsed.Before(b.Metrics.LastUsed)
		}
		return a.ID < b.ID
	})
	return out
}

func healthRank(h Health) int {
	switch h {
	case HealthHealthy:
		return 0
	case HealthUnknown:
		return 1
	case HealthDegraded:
		return 2
	default:
		return 3
	}
}

// Select returns the best available resource for the filter WITHOUT reserving it (a scheduling
// preview). ok=false if none is available (the failover set is empty).
func (m *Manager) Select(f Filter) (*Resource, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.candidates(f)
	if len(c) == 0 {
		return nil, false
	}
	return c[0].clone(), true
}

// Reserve atomically selects the best available resource for the filter and marks it reserved by
// holder (transient — NOT a durable lock; S7B owns durable ownership). It naturally fails over past
// unhealthy/cooling/already-reserved resources. ok=false if none is available.
func (m *Manager) Reserve(holder string, f Filter) (*Resource, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.candidates(f)
	if len(c) == 0 {
		return nil, false
	}
	r := c[0]
	r.reservedBy = holder
	r.Metrics.Reservations++
	r.Metrics.LastUsed = m.now()
	return r.clone(), true
}

// ReserveID reserves one specific resource by ID. ok=false if it is missing or not Available.
func (m *Manager) ReserveID(id, holder string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[id]
	if !ok || m.availabilityOf(r) != Available {
		return false
	}
	r.reservedBy = holder
	r.Metrics.Reservations++
	r.Metrics.LastUsed = m.now()
	return true
}

// Release frees a reservation, making the resource available again. Returns false if it was not
// reserved.
func (m *Manager) Release(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.res[id]
	if !ok || r.reservedBy == "" {
		return false
	}
	r.reservedBy = ""
	return true
}

// RecordUse marks a successful use (metrics).
func (m *Manager) RecordUse(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.res[id]; ok {
		r.Metrics.Uses++
		r.Metrics.LastUsed = m.now()
	}
}

// RecordFailure marks a failure (metrics); repeated failures may be reflected in health by callers.
func (m *Manager) RecordFailure(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.res[id]; ok {
		r.Metrics.Failures++
	}
}

// Snapshot is one resource's observable state for dashboards/metrics.
type Snapshot struct {
	Resource
	Availability Availability `json:"availability"`
	ReservedBy   string       `json:"reserved_by,omitempty"`
}

// Snapshot returns the observable state of every resource, sorted by ID (deterministic).
func (m *Manager) Snapshot() []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, len(m.res))
	for _, r := range m.res {
		out = append(out, Snapshot{Resource: *r.clone(), Availability: m.availabilityOf(r), ReservedBy: r.reservedBy})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
