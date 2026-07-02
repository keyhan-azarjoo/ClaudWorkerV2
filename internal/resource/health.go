package resource

// SetHealth records a resource's health (health monitoring). Unknown resources are ignored.
func (m *Manager) SetHealth(id string, h Health) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.res[id]; ok {
		r.Health = h
		r.Metrics.LastHealthAt = m.now()
	}
}

// SetUsage records a resource's last-known usage percent (input to pacing/scheduling; the pause RULE
// itself lives in the Policy Engine's BudgetPolicy, S6).
func (m *Manager) SetUsage(id string, usagePct int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.res[id]; ok {
		r.Metrics.UsagePct = usagePct
	}
}

// Discoverer finds resources of some kind (discovery). Implementations wrap a real probe (adb, a
// claude account list, git worktree list, …). It returns the current set; the Manager reconciles.
type Discoverer interface {
	Discover() ([]Resource, error)
}

// StaticDiscoverer yields a fixed set — for declared resources, tests, and as the trivial default
// before live probers are wired.
type StaticDiscoverer struct{ Resources []Resource }

// Discover returns the static set.
func (d StaticDiscoverer) Discover() ([]Resource, error) { return d.Resources, nil }

// Discover runs a discoverer and registers everything it finds. Existing resources with the same ID
// are refreshed in place — preserving their live metrics/reservation — while identity fields
// (kind/name/labels) and health are updated from discovery. Newly-seen resources are added.
func (m *Manager) Discover(d Discoverer) error {
	found, err := d.Discover()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, fr := range found {
		if existing, ok := m.res[fr.ID]; ok {
			existing.Kind = fr.Kind
			existing.Name = fr.Name
			existing.Labels = fr.clone().Labels
			if fr.Health != "" && fr.Health != HealthUnknown {
				existing.Health = fr.Health
				existing.Metrics.LastHealthAt = m.now()
			}
			continue
		}
		nr := fr.clone()
		if nr.Health == "" {
			nr.Health = HealthUnknown
		}
		m.res[nr.ID] = nr
	}
	return nil
}
