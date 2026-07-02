package lease

import (
	"fmt"
	"sync"
	"time"
)

// DefaultTTL values per kind (used when a Request does not override TTL). Chosen so a crashed owner's
// lease lapses within a work cycle, letting another Assignment reclaim the resource automatically.
var DefaultTTL = map[Kind]time.Duration{
	KindIssue:    30 * time.Minute,
	KindResource: 15 * time.Minute,
	KindMerge:    10 * time.Minute,
}

const fallbackTTL = 15 * time.Minute

// Manager owns lease lifecycle over a durable Store. It is deterministic: every decision is a pure
// function of the persisted leases plus the injected clock. It holds no state beyond the store and a
// mutex serialising acquire/transfer races.
type Manager struct {
	store Store
	now   func() time.Time
	ttl   map[Kind]time.Duration
	mu    sync.Mutex
}

// Option configures a Manager.
type Option func(*Manager)

// WithClock overrides the time source (tests inject a controllable clock).
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// WithTTL overrides the default TTL for a kind.
func WithTTL(k Kind, d time.Duration) Option {
	return func(m *Manager) { m.ttl[k] = d }
}

// New returns a Manager backed by store.
func New(store Store, opts ...Option) *Manager {
	m := &Manager{store: store, now: time.Now, ttl: map[Kind]time.Duration{}}
	for k, v := range DefaultTTL {
		m.ttl[k] = v
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *Manager) ttlFor(k Kind) time.Duration {
	if d, ok := m.ttl[k]; ok && d > 0 {
		return d
	}
	return fallbackTTL
}

// leaseID is the deterministic identity of a (kind, resource) lease. Exactly one active lease may
// exist per pair.
func leaseID(k Kind, resource string) string { return string(k) + "/" + resource }

// Request describes a lease to acquire.
type Request struct {
	Kind      Kind
	Resource  string
	Owner     string        // the owning Assignment (issue key)
	Reason    string        // observability
	Renewable bool          // may this lease be renewed?
	TTL       time.Duration // 0 → default for the kind
}

// Acquire grants a lease for (kind, resource) to Owner. It succeeds when the resource is free — i.e.
// no lease exists, the existing lease has EXPIRED (automatic reclamation, no human step), or the
// existing active lease is already Owner's (idempotent). It fails (ok=false) only when a DIFFERENT
// owner holds an active lease.
func (m *Manager) Acquire(r Request) (*Lease, bool, error) {
	if !r.Kind.Valid() {
		return nil, false, fmt.Errorf("lease: invalid kind %q", r.Kind)
	}
	if r.Resource == "" || r.Owner == "" {
		return nil, false, fmt.Errorf("lease: resource and owner are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	id := leaseID(r.Kind, r.Resource)
	now := m.now()
	existing, ok, err := m.store.Load(id)
	if err != nil {
		return nil, false, err
	}
	if ok && existing.Active(now) {
		if existing.Owner == r.Owner {
			return existing.clone(), true, nil // idempotent: already ours
		}
		return existing.clone(), false, nil // held by someone else, still valid
	}
	// free or expired → grant (reclaims an expired lease deterministically)
	ttl := r.TTL
	if ttl <= 0 {
		ttl = m.ttlFor(r.Kind)
	}
	l := &Lease{
		ID: id, Kind: r.Kind, Resource: r.Resource, Owner: r.Owner,
		CreatedAt: now, ExpiresAt: now.Add(ttl), Renewable: r.Renewable, Reason: r.Reason,
	}
	if err := m.store.Save(l); err != nil {
		return nil, false, err
	}
	return l.clone(), true, nil
}

// Renew extends an ACTIVE lease owned by owner (renewal). It fails if the lease is missing, expired,
// owned by someone else, or not renewable. TTL 0 → default for the kind.
func (m *Manager) Renew(k Kind, resource, owner string, ttl time.Duration) (*Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := leaseID(k, resource)
	l, ok, err := m.store.Load(id)
	if err != nil || !ok {
		return nil, false, err
	}
	now := m.now()
	if !l.Active(now) || l.Owner != owner || !l.Renewable {
		return nil, false, nil
	}
	if ttl <= 0 {
		ttl = m.ttlFor(k)
	}
	l.ExpiresAt = now.Add(ttl)
	if err := m.store.Save(l); err != nil {
		return nil, false, err
	}
	return l.clone(), true, nil
}

// Release frees a lease held by owner (deletes the record). It is a no-op returning ok=false if the
// lease is missing or owned by someone else. Releasing is safe after expiry too.
func (m *Manager) Release(k Kind, resource, owner string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := leaseID(k, resource)
	l, ok, err := m.store.Load(id)
	if err != nil || !ok {
		return false, err
	}
	if l.Owner != owner {
		return false, nil
	}
	return true, m.store.Delete(id)
}

// Transfer reassigns an ACTIVE lease from one owner to another (ownership transfer), e.g. on
// escalation or reassignment. It resets expiry from now and updates the reason. Fails if the lease is
// missing, expired, or not currently owned by `from`.
func (m *Manager) Transfer(k Kind, resource, from, to, reason string) (*Lease, bool, error) {
	if to == "" {
		return nil, false, fmt.Errorf("lease: transfer target owner is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := leaseID(k, resource)
	l, ok, err := m.store.Load(id)
	if err != nil || !ok {
		return nil, false, err
	}
	now := m.now()
	if !l.Active(now) || l.Owner != from {
		return nil, false, nil
	}
	l.Owner = to
	l.ExpiresAt = now.Add(m.ttlFor(k))
	if reason != "" {
		l.Reason = reason
	}
	if err := m.store.Save(l); err != nil {
		return nil, false, err
	}
	return l.clone(), true, nil
}

// Validate reports whether owner currently holds a valid (active) lease on (kind, resource). This is
// the check the engine uses before acting on a leased resource.
func (m *Manager) Validate(k Kind, resource, owner string) (bool, error) {
	l, ok, err := m.store.Load(leaseID(k, resource))
	if err != nil || !ok {
		return false, err
	}
	return l.Active(m.now()) && l.Owner == owner, nil
}

// Get returns the lease for (kind, resource) if present (active or not).
func (m *Manager) Get(k Kind, resource string) (*Lease, bool, error) {
	return m.store.Load(leaseID(k, resource))
}

// Active returns every currently-valid lease (expired ones excluded), sorted by ID.
func (m *Manager) Active() ([]*Lease, error) {
	all, err := m.store.List()
	if err != nil {
		return nil, err
	}
	now := m.now()
	out := make([]*Lease, 0, len(all))
	for _, l := range all {
		if l.Active(now) {
			out = append(out, l)
		}
	}
	return out, nil
}

// Reap deletes all EXPIRED leases (automatic cleanup / resource reclamation). It is deterministic and
// needs no human intervention; it is optional housekeeping since validity is always derived from
// ExpiresAt regardless. Returns the number reaped.
func (m *Manager) Reap() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all, err := m.store.List()
	if err != nil {
		return 0, err
	}
	now := m.now()
	n := 0
	for _, l := range all {
		if !l.Active(now) {
			if err := m.store.Delete(l.ID); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
