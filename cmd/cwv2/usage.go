package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/config"
)

// usageMonitor reports each account's real subscription usage (5-hour session + 7-day week %, with
// reset times) — the same data V1's panel showed. It shells out to the usage probe (path in
// CWV2_USAGE_PROBE) per account config dir. Probing is slow (drives the CLI), so results are cached
// and only refreshed on demand.
type usageMonitor struct {
	script   string
	accounts []config.Account

	mu    sync.Mutex
	cache map[string]usageEntry // by account name
}

type usageEntry struct {
	SessionPct   int    `json:"session_pct"`   // 5-hour utilization %
	WeekPct      int    `json:"week_pct"`      // 7-day utilization %
	SessionReset string `json:"session_reset"` // human reset ("11:50pm (Europe/London)")
	WeekReset    string `json:"week_reset"`    //
	SessionMin   int    `json:"session_min"`   // minutes to reset
	WeekMin      int    `json:"week_min"`      //
	OK           bool   `json:"ok"`            // probe returned real numbers
	At           string `json:"at"`            // when this entry was refreshed (RFC3339)
}

func newUsageMonitor(accounts []config.Account) *usageMonitor {
	return &usageMonitor{script: os.Getenv("CWV2_USAGE_PROBE"), accounts: accounts, cache: map[string]usageEntry{}}
}

// snapshot returns the cached usage by account name.
func (u *usageMonitor) snapshot() map[string]usageEntry {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]usageEntry, len(u.cache))
	for k, v := range u.cache {
		out[k] = v
	}
	return out
}

// refresh probes every claude account and updates the cache. Returns the fresh snapshot.
func (u *usageMonitor) refresh(ctx context.Context, now time.Time) map[string]usageEntry {
	if u.script == "" {
		return u.snapshot()
	}
	home, _ := os.UserHomeDir()
	for _, a := range u.accounts {
		if a.Engine != "" && a.Engine != "claude" {
			continue
		}
		dir := a.ConfigDir
		if len(dir) >= 2 && dir[:2] == "~/" && home != "" {
			dir = home + dir[1:]
		}
		e := u.probe(ctx, dir)
		e.At = now.UTC().Format(time.RFC3339)
		u.mu.Lock()
		u.cache[a.Name] = e
		u.mu.Unlock()
	}
	return u.snapshot()
}

// probe runs the usage probe for one config dir (bounded by a timeout).
func (u *usageMonitor) probe(ctx context.Context, configDir string) usageEntry {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "python3", u.script, configDir).Output()
	if err != nil {
		return usageEntry{}
	}
	var raw struct {
		OK              bool   `json:"ok"`
		Session         int    `json:"session"`
		Week            int    `json:"week"`
		SessionResetMin int    `json:"sessionResetMin"`
		WeekResetMin    int    `json:"weekResetMin"`
		SessionReset    string `json:"sessionReset"`
		WeekReset       string `json:"weekReset"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return usageEntry{}
	}
	return usageEntry{
		SessionPct: raw.Session, WeekPct: raw.Week,
		SessionReset: raw.SessionReset, WeekReset: raw.WeekReset,
		SessionMin: raw.SessionResetMin, WeekMin: raw.WeekResetMin, OK: raw.OK,
	}
}
