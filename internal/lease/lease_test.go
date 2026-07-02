package lease

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }

// clockAt returns a controllable clock starting at t; advance() moves it forward.
type clk struct{ t time.Time }

func (c *clk) now() time.Time      { return c.t }
func (c *clk) adv(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clk { return &clk{t: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)} }

func managers(t *testing.T, c *clk) map[string]*Manager {
	fs, err := NewFileStore(filepath.Join(t.TempDir(), "leases"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]*Manager{
		"file":   New(fs, WithClock(c.now)),
		"memory": New(NewMemoryStore(), WithClock(c.now)),
	}
}

func TestAcquireExclusiveWhileActive(t *testing.T) {
	for name, m := range managers(t, newClock()) {
		t.Run(name, func(t *testing.T) {
			l, ok, err := m.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "SCRUM-1", Reason: "claim"})
			if err != nil || !ok {
				t.Fatalf("first acquire: ok=%v err=%v", ok, err)
			}
			if l.Resource != "SCRUM-1" || l.Owner != "SCRUM-1" || l.CreatedAt.IsZero() || !l.ExpiresAt.After(l.CreatedAt) {
				t.Errorf("lease fields = %+v", l)
			}
			// a different owner cannot take it while active
			if _, ok, _ := m.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "OTHER"}); ok {
				t.Error("second owner acquired an active lease")
			}
			// same owner is idempotent
			if _, ok, _ := m.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "SCRUM-1"}); !ok {
				t.Error("same-owner re-acquire should be idempotent-ok")
			}
		})
	}
}

func TestExpirationFreesOwnership(t *testing.T) {
	c := newClock()
	for name, m := range managers(t, c) {
		t.Run(name, func(t *testing.T) {
			cc := &clk{t: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)}
			m = New(NewMemoryStore(), WithClock(cc.now))
			_, ok, _ := m.Acquire(Request{Kind: KindResource, Resource: "acct-a", Owner: "SCRUM-1", TTL: time.Minute})
			if !ok {
				t.Fatal("acquire failed")
			}
			if v, _ := m.Validate(KindResource, "acct-a", "SCRUM-1"); !v {
				t.Error("should be valid before expiry")
			}
			cc.adv(2 * time.Minute) // past TTL
			if v, _ := m.Validate(KindResource, "acct-a", "SCRUM-1"); v {
				t.Error("must be invalid after expiry")
			}
			act, _ := m.Active()
			if len(act) != 0 {
				t.Errorf("Active should exclude expired: %v", act)
			}
			_ = name
		})
	}
}

// TestCrashRecoveryReclamation: owner A takes a lease and "crashes" (never releases). After expiry,
// owner B acquires with NO human intervention (automatic reclamation, deterministic).
func TestCrashRecoveryReclamation(t *testing.T) {
	c := newClock()
	m := New(NewMemoryStore(), WithClock(c.now), WithTTL(KindResource, time.Minute))
	if _, ok, _ := m.Acquire(Request{Kind: KindResource, Resource: "acct-a", Owner: "A", Reason: "A working"}); !ok {
		t.Fatal("A acquire failed")
	}
	// A crashes; time passes beyond TTL.
	c.adv(90 * time.Second)
	l, ok, err := m.Acquire(Request{Kind: KindResource, Resource: "acct-a", Owner: "B", Reason: "B reclaims"})
	if err != nil || !ok {
		t.Fatalf("B should reclaim expired lease: ok=%v err=%v", ok, err)
	}
	if l.Owner != "B" {
		t.Errorf("reclaimed owner = %q, want B", l.Owner)
	}
}

// TestRestartFromDisk: a brand-new Manager+FileStore over the same dir recovers persisted ownership
// (restart safety) — active leases remain owned; expired ones are reclaimable.
func TestRestartFromDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leases")
	c := newClock()

	fs1, _ := NewFileStore(dir)
	m1 := New(fs1, WithClock(c.now), WithTTL(KindIssue, 10*time.Minute))
	if _, ok, _ := m1.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "SCRUM-1", Reason: "claim"}); !ok {
		t.Fatal("acquire failed")
	}

	// "restart": new store + manager over the same dir, reload purely from disk.
	fs2, _ := NewFileStore(dir)
	m2 := New(fs2, WithClock(c.now), WithTTL(KindIssue, 10*time.Minute))
	if v, _ := m2.Validate(KindIssue, "SCRUM-1", "SCRUM-1"); !v {
		t.Error("ownership not recovered after restart")
	}
	// another owner still cannot steal the active recovered lease
	if _, ok, _ := m2.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "OTHER"}); ok {
		t.Error("recovered active lease was stolen")
	}
	// after expiry it becomes reclaimable post-restart
	c.adv(11 * time.Minute)
	if _, ok, _ := m2.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "OTHER"}); !ok {
		t.Error("expired lease not reclaimable after restart")
	}
}

func TestRenewExtendsActiveOnly(t *testing.T) {
	c := newClock()
	m := New(NewMemoryStore(), WithClock(c.now))
	_, _, _ = m.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "SCRUM-1", Renewable: true, TTL: 10 * time.Minute})
	c.adv(5 * time.Minute)
	l, ok, _ := m.Renew(KindIssue, "SCRUM-1", "SCRUM-1", 10*time.Minute)
	if !ok || !l.ExpiresAt.Equal(c.now().Add(10*time.Minute)) {
		t.Errorf("renew did not extend: ok=%v exp=%v", ok, l)
	}
	// non-renewable lease cannot be renewed
	m.Release(KindIssue, "SCRUM-1", "SCRUM-1")
	_, _, _ = m.Acquire(Request{Kind: KindIssue, Resource: "X", Owner: "o", Renewable: false, TTL: time.Minute})
	if _, ok, _ := m.Renew(KindIssue, "X", "o", time.Minute); ok {
		t.Error("non-renewable lease should not renew")
	}
	// expired lease cannot be renewed
	c.adv(2 * time.Minute)
	if _, ok, _ := m.Renew(KindIssue, "X", "o", time.Minute); ok {
		t.Error("expired lease should not renew")
	}
}

func TestReleaseOnlyByOwner(t *testing.T) {
	m := New(NewMemoryStore(), WithClock(newClock().now))
	_, _, _ = m.Acquire(Request{Kind: KindMerge, Resource: "development", Owner: "SCRUM-1"})
	if ok, _ := m.Release(KindMerge, "development", "OTHER"); ok {
		t.Error("non-owner released a lease")
	}
	if ok, _ := m.Release(KindMerge, "development", "SCRUM-1"); !ok {
		t.Error("owner should release its lease")
	}
	// after release, free to acquire
	if _, ok, _ := m.Acquire(Request{Kind: KindMerge, Resource: "development", Owner: "OTHER"}); !ok {
		t.Error("released lease not re-acquirable")
	}
}

func TestTransferOwnership(t *testing.T) {
	c := newClock()
	m := New(NewMemoryStore(), WithClock(c.now))
	_, _, _ = m.Acquire(Request{Kind: KindIssue, Resource: "SCRUM-1", Owner: "A", TTL: 10 * time.Minute})
	l, ok, _ := m.Transfer(KindIssue, "SCRUM-1", "A", "B", "escalation")
	if !ok || l.Owner != "B" || l.Reason != "escalation" {
		t.Fatalf("transfer = %+v ok=%v", l, ok)
	}
	if v, _ := m.Validate(KindIssue, "SCRUM-1", "B"); !v {
		t.Error("B should own after transfer")
	}
	// wrong 'from' fails
	if _, ok, _ := m.Transfer(KindIssue, "SCRUM-1", "A", "C", "x"); ok {
		t.Error("transfer from wrong owner should fail")
	}
}

func TestReapDeletesExpired(t *testing.T) {
	c := newClock()
	m := New(NewMemoryStore(), WithClock(c.now))
	_, _, _ = m.Acquire(Request{Kind: KindResource, Resource: "a", Owner: "o", TTL: time.Minute})
	_, _, _ = m.Acquire(Request{Kind: KindResource, Resource: "b", Owner: "o", TTL: time.Hour})
	c.adv(2 * time.Minute) // 'a' expired, 'b' still active
	n, err := m.Reap()
	if err != nil || n != 1 {
		t.Fatalf("reap = %d err=%v, want 1", n, err)
	}
	if _, ok, _ := m.Get(KindResource, "a"); ok {
		t.Error("expired lease 'a' should be reaped")
	}
	if _, ok, _ := m.Get(KindResource, "b"); !ok {
		t.Error("active lease 'b' must remain")
	}
}

func TestValidationRejectsBadInput(t *testing.T) {
	m := New(NewMemoryStore(), WithClock(newClock().now))
	if _, _, err := m.Acquire(Request{Kind: "bogus", Resource: "r", Owner: "o"}); err == nil {
		t.Error("invalid kind should error")
	}
	if _, _, err := m.Acquire(Request{Kind: KindIssue, Owner: "o"}); err == nil {
		t.Error("missing resource should error")
	}
}

func TestStoreRejectsNewerFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "issue_SCRUM-9.json")
	if err := writeFile(p, `{"id":"issue/SCRUM-9","kind":"issue","resource":"SCRUM-9","owner":"SCRUM-9","spec_version":999}`); err != nil {
		t.Fatal(err)
	}
	s, _ := NewFileStore(dir)
	if _, _, err := s.Load("issue/SCRUM-9"); err == nil {
		t.Error("newer format must be rejected, not silently ignored")
	}
}
