package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// probe fetches one account's LIVE subscription usage from Anthropic's OAuth usage endpoint — the same
// data as the in-app /usage page (5-hour + 7-day % used + reset times). Fully headless: no browser, no
// interactive CLI. utilization = % USED.
func (u *usageMonitor) probe(ctx context.Context, configDir string) usageEntry {
	tok := oauthAccessToken(configDir)
	if tok == "" {
		return usageEntry{}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return usageEntry{}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return usageEntry{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return usageEntry{}
	}
	var raw struct {
		FiveHour window `json:"five_hour"`
		SevenDay window `json:"seven_day"`
	}
	if json.NewDecoder(resp.Body).Decode(&raw) != nil {
		return usageEntry{}
	}
	return usageEntry{
		SessionPct:   int(raw.FiveHour.Utilization + 0.5),
		WeekPct:      int(raw.SevenDay.Utilization + 0.5),
		SessionReset: humanReset(raw.FiveHour.ResetsAt),
		WeekReset:    humanReset(raw.SevenDay.ResetsAt),
		SessionMin:   minsUntil(raw.FiveHour.ResetsAt),
		WeekMin:      minsUntil(raw.SevenDay.ResetsAt),
		OK:           true,
	}
}

type window struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// oauthAccessToken resolves an account's OAuth access token headlessly: the per-account credentials file
// if present, else the macOS login keychain (service "Claude Code-credentials", read with no GUI prompt).
func oauthAccessToken(configDir string) string {
	parse := func(b []byte) string {
		var c struct {
			ClaudeAiOauth struct {
				AccessToken string `json:"accessToken"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(b, &c) == nil {
			return c.ClaudeAiOauth.AccessToken
		}
		return ""
	}
	if configDir != "" {
		if b, err := os.ReadFile(filepath.Join(configDir, ".credentials.json")); err == nil {
			if t := parse(b); t != "" {
				return t
			}
		}
	}
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return ""
	}
	return parse(out)
}

// minsUntil returns whole minutes from now until an ISO timestamp (0 if past/unparseable).
func minsUntil(iso string) int {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}
	if d := time.Until(t); d > 0 {
		return int(d.Minutes())
	}
	return 0
}

// humanReset formats an ISO reset time in local time (e.g. "Mon 9:59am").
func humanReset(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return t.Local().Format("Mon 3:04pm")
}
