package resource

import (
	"testing"
	"time"
)

func steadyClock() func() time.Time {
	t := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		cur := t
		t = t.Add(time.Second)
		return cur
	}
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func acct(id string, usage int) Resource {
	return Resource{ID: id, Kind: KindClaudeAccount, Name: id, Health: HealthHealthy, Metrics: Metrics{UsagePct: usage}}
}

func TestRegisterGetListDeregister(t *testing.T) {
	m := New(WithClock(steadyClock()))
	m.Register(acct("a", 10))
	m.Register(acct("b", 20))
	if _, ok := m.Get("a"); !ok {
		t.Fatal("a missing")
	}
	if got := m.List(Filter{Kind: KindClaudeAccount}); len(got) != 2 || got[0].ID != "a" {
		t.Errorf("list = %v", got)
	}
	if got := m.List(Filter{Kind: KindGitWorktree}); len(got) != 0 {
		t.Errorf("filter by kind failed: %v", got)
	}
	if !m.Deregister("a") || m.Deregister("a") {
		t.Error("deregister semantics wrong")
	}
}

func TestRegisterDefaultsHealthUnknown(t *testing.T) {
	m := New()
	m.Register(Resource{ID: "x", Kind: KindLocalRuntime})
	r, _ := m.Get("x")
	if r.Health != HealthUnknown {
		t.Errorf("default health = %q, want unknown", r.Health)
	}
}

func TestGetReturnsCopy(t *testing.T) {
	m := New()
	m.Register(Resource{ID: "x", Kind: KindMacMini, Labels: map[string]string{"os": "darwin"}})
	r, _ := m.Get("x")
	r.Labels["os"] = "MUTATED"
	r.Name = "MUTATED"
	again, _ := m.Get("x")
	if again.Labels["os"] != "darwin" || again.Name != "" {
		t.Error("Get did not return an isolated copy")
	}
}

func TestHealthAndUsage(t *testing.T) {
	m := New(WithClock(steadyClock()))
	m.Register(acct("a", 0))
	m.SetHealth("a", HealthDegraded)
	m.SetUsage("a", 77)
	r, _ := m.Get("a")
	if r.Health != HealthDegraded || r.Metrics.UsagePct != 77 || r.Metrics.LastHealthAt.IsZero() {
		t.Errorf("health/usage not recorded: %+v", r)
	}
}

func TestAvailabilityPrecedence(t *testing.T) {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	m := New(WithClock(fixedClock(base)))
	m.Register(acct("a", 0))

	// down → offline (beats everything)
	m.SetHealth("a", HealthDown)
	m.Cooldown("a", base.Add(time.Hour))
	if av, _ := m.AvailabilityOf("a"); av != Offline {
		t.Errorf("down = %q, want offline", av)
	}
	// healthy + cooling → cooldown
	m.SetHealth("a", HealthHealthy)
	if av, _ := m.AvailabilityOf("a"); av != Cooldown {
		t.Errorf("cooling = %q, want cooldown", av)
	}
	// past cooldown → available
	m.Cooldown("a", base.Add(-time.Hour))
	if av, _ := m.AvailabilityOf("a"); av != Available {
		t.Errorf("expired cooldown = %q, want available", av)
	}
	// reserved → reserved
	m.ReserveID("a", "holder")
	if av, _ := m.AvailabilityOf("a"); av != Reserved {
		t.Errorf("reserved = %q, want reserved", av)
	}
}

func TestReserveReleaseTransient(t *testing.T) {
	m := New(WithClock(steadyClock()))
	m.Register(acct("a", 0))
	if !m.ReserveID("a", "h1") {
		t.Fatal("first reserve should succeed")
	}
	if m.ReserveID("a", "h2") {
		t.Error("second reserve must fail while reserved")
	}
	if !m.Release("a") || m.Release("a") {
		t.Error("release semantics wrong")
	}
	if !m.ReserveID("a", "h3") {
		t.Error("reserve after release should succeed")
	}
	r, _ := m.Get("a")
	if r.Metrics.Reservations != 2 {
		t.Errorf("reservations = %d, want 2", r.Metrics.Reservations)
	}
}

// TestSchedulingOrder proves deterministic selection: healthiest, then lowest usage, then LRU, then id.
func TestSchedulingOrder(t *testing.T) {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	m := New(WithClock(fixedClock(base)))
	// same health, different usage → lower usage wins
	m.Register(acct("high", 90))
	m.Register(acct("low", 10))
	if r, ok := m.Reserve("h", Filter{Kind: KindClaudeAccount}); !ok || r.ID != "low" {
		t.Fatalf("selected %v, want low (lower usage)", r)
	}
	// degraded is deprioritised even at lower usage
	m2 := New(WithClock(fixedClock(base)))
	deg := acct("deg", 0)
	deg.Health = HealthDegraded
	m2.Register(deg)
	m2.Register(acct("ok", 50))
	if r, ok := m2.Reserve("h", Filter{Kind: KindClaudeAccount}); !ok || r.ID != "ok" {
		t.Fatalf("selected %v, want ok (healthy beats degraded)", r)
	}
}

// TestFailoverSkipsUnavailable proves reservation fails over past reserved/cooling/down resources.
func TestFailoverSkipsUnavailable(t *testing.T) {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	m := New(WithClock(fixedClock(base)))
	m.Register(acct("a", 10)) // best by usage, but we'll make it unavailable
	m.Register(acct("b", 20))
	m.Register(acct("c", 30))
	m.ReserveID("a", "someone")          // a reserved
	m.Cooldown("b", base.Add(time.Hour)) // b cooling
	r, ok := m.Reserve("h", Filter{Kind: KindClaudeAccount})
	if !ok || r.ID != "c" {
		t.Fatalf("failover selected %v, want c", r)
	}
	// everything unavailable → no selection
	m.SetHealth("c", HealthDown)
	m.Release("a")
	m.ReserveID("a", "again")
	if _, ok := m.Reserve("h", Filter{Kind: KindClaudeAccount}); ok {
		t.Error("expected no available resource")
	}
}

func TestLRURotation(t *testing.T) {
	m := New(WithClock(steadyClock())) // advances 1s per call
	m.Register(acct("a", 0))
	m.Register(acct("b", 0)) // equal usage → LRU + id tiebreak
	// first reserve: a and b both LastUsed zero → id tiebreak → a
	r1, _ := m.Reserve("h", Filter{Kind: KindClaudeAccount})
	m.Release(r1.ID)
	// now a has a newer LastUsed → b is least-recently-used → b
	r2, _ := m.Reserve("h", Filter{Kind: KindClaudeAccount})
	if r1.ID != "a" || r2.ID != "b" {
		t.Errorf("LRU rotation = %s then %s, want a then b", r1.ID, r2.ID)
	}
}

func TestDiscoverReconcilesPreservingMetrics(t *testing.T) {
	m := New(WithClock(steadyClock()))
	m.Register(acct("a", 10))
	m.RecordUse("a")           // give 'a' live metrics
	m.ReserveID("a", "holder") // and a live reservation
	d := StaticDiscoverer{Resources: []Resource{
		{ID: "a", Kind: KindClaudeAccount, Name: "a-renamed", Health: HealthHealthy, Labels: map[string]string{"region": "eu"}},
		{ID: "z", Kind: KindClaudeAccount, Name: "z", Health: HealthHealthy},
	}}
	if err := m.Discover(d); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Get("a")
	if a.Name != "a-renamed" || a.Labels["region"] != "eu" {
		t.Errorf("discovery did not refresh identity: %+v", a)
	}
	if a.Metrics.Uses != 1 {
		t.Errorf("discovery clobbered live metrics: %+v", a.Metrics)
	}
	if av, _ := m.AvailabilityOf("a"); av != Reserved {
		t.Errorf("discovery clobbered reservation: %v", av)
	}
	if _, ok := m.Get("z"); !ok {
		t.Error("discovery did not add new resource z")
	}
}

func TestSnapshot(t *testing.T) {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	m := New(WithClock(fixedClock(base)))
	m.Register(acct("a", 0))
	m.ReserveID("a", "holder")
	s := m.Snapshot()
	if len(s) != 1 || s[0].Availability != Reserved || s[0].ReservedBy != "holder" {
		t.Errorf("snapshot = %+v", s)
	}
}
