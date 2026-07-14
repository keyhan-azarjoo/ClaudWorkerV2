package aiworkspace

import (
	"context"
	"testing"
)

func TestCacheHitMissAndRatio(t *testing.T) {
	c := newCacheStore(t.TempDir())
	key := HashKey("optimizer", "markdown", "cfg", "hello")

	if _, _, hit := c.Get(key); hit {
		t.Fatal("expected miss on empty cache")
	}
	c.Put("optimizer", key, "label", []byte("world"), 3)
	e, val, hit := c.Get(key)
	if !hit || string(val) != "world" || e.Hits != 1 {
		t.Fatalf("expected hit with value+hit count, got hit=%v val=%q hits=%d", hit, val, e.Hits)
	}
	// 1 miss + 2 gets (1 hit recorded above) → after another hit: hits=2, misses=1 → ratio 66.
	c.Get(key)
	st := c.Stats()
	if st["hits"].(int) != 2 || st["misses"].(int) != 1 {
		t.Fatalf("counters wrong: %+v", st)
	}
	if st["hitRatio"].(int) != 66 {
		t.Fatalf("expected ratio 66, got %v", st["hitRatio"])
	}
}

func TestCacheClearKeepsPinned(t *testing.T) {
	c := newCacheStore(t.TempDir())
	k1 := HashKey("a")
	k2 := HashKey("b")
	c.Put("optimizer", k1, "one", []byte("x"), 1)
	c.Put("optimizer", k2, "two", []byte("y"), 1)
	c.Pin(k2, true)
	removed := c.Clear("")
	if removed != 1 {
		t.Fatalf("expected 1 removed (pinned kept), got %d", removed)
	}
	if _, _, hit := c.Get(k2); !hit {
		t.Fatal("pinned entry should survive clear")
	}
}

// TestOptimizerCacheServesSecondRun — the service caches optimizer output so an identical rerun is a hit.
func TestOptimizerCacheServesSecondRun(t *testing.T) {
	svc := New(t.TempDir())
	content := "# Doc\n\n\n<!-- comment -->\nbody\n\n\n"
	r1, err := svc.RunOptimizer(context.Background(), "markdown", "markdown", content, nil)
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if r1["cached"].(bool) {
		t.Fatal("first run must not be cached")
	}
	r2, _ := svc.RunOptimizer(context.Background(), "markdown", "markdown", content, nil)
	if !r2["cached"].(bool) {
		t.Fatal("second identical run must be served from cache")
	}
	if r1["output"].(string) != r2["output"].(string) {
		t.Fatal("cached output must match")
	}
	if svc.Dashboard()["cacheHitRatio"].(int) <= 0 {
		t.Fatal("dashboard cache hit ratio should be > 0 after a cache hit")
	}
}

// TestUsageSeriesGroups — recorded optimizer savings show up in the by-optimizer breakdown and totals.
func TestUsageSeriesGroups(t *testing.T) {
	svc := New(t.TempDir())
	svc.RecordUsage(UsageEvent{Optimizer: "markdown", SavedTok: 40})
	svc.RecordUsage(UsageEvent{Optimizer: "markdown", SavedTok: 10})
	svc.RecordUsage(UsageEvent{Optimizer: "dedup", SavedTok: 25})
	s := svc.UsageSeries(30)
	if len(s.Days) != 30 {
		t.Fatalf("expected 30-day window, got %d", len(s.Days))
	}
	if s.TotalSaved != 75 || s.Events != 3 {
		t.Fatalf("totals wrong: saved=%d events=%d", s.TotalSaved, s.Events)
	}
	if len(s.ByOptimizer) == 0 || s.ByOptimizer[0].Name != "markdown" || s.ByOptimizer[0].Value != 50 {
		t.Fatalf("by-optimizer breakdown wrong: %+v", s.ByOptimizer)
	}
}
