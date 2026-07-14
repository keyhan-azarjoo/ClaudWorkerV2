package aiworkspace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// UsageEvent is one recorded AI interaction (or optimizer run). Producers in later phases (optimizers,
// the optimizing proxy) append these; Phase 1 ships the store + rollup so the Dashboard has a real,
// honest source (empty until something records). Money figures are ESTIMATES.
type UsageEvent struct {
	TS          string  `json:"ts"`
	Provider    string  `json:"provider,omitempty"`
	Account     string  `json:"account,omitempty"`
	Model       string  `json:"model,omitempty"`
	Workspace   string  `json:"workspace,omitempty"`
	Optimizer   string  `json:"optimizer,omitempty"`
	InputTok    int     `json:"inputTok"`
	OutputTok   int     `json:"outputTok"`
	ContextTok  int     `json:"contextTok"`
	CachedTok   int     `json:"cachedTok"`
	SavedTok    int     `json:"savedTok"`
	CostEstUSD  float64 `json:"costEstUSD"`
	SavedEstUSD float64 `json:"savedEstUSD"`
}

// UsageSummary is the Dashboard's usage rollup.
type UsageSummary struct {
	TodayTokens int     `json:"todayTokens"`
	MonthTokens int     `json:"monthTokens"`
	TodaySaved  int     `json:"todaySaved"`
	MonthSaved  int     `json:"monthSaved"`
	MonthCost   float64 `json:"monthCostEstUSD"`
	MonthSavedU float64 `json:"monthSavedEstUSD"`
	Events      int     `json:"events"`
	// Sparklines: last 14 days of total tokens (oldest→newest) for the dashboard chart.
	Days []DayPoint `json:"days"`
}

// DayPoint is one day's totals for the sparkline.
type DayPoint struct {
	Date   string `json:"date"` // YYYY-MM-DD
	Tokens int    `json:"tokens"`
	Saved  int    `json:"saved"`
}

// NameVal is a labelled total for breakdown charts.
type NameVal struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// UsageSeries powers the Usage analytics page: daily totals plus breakdowns. Provider/model breakdowns
// stay empty until the optimizing proxy (a later phase) records inference events — honest, not faked.
type UsageSeries struct {
	RangeDays   int        `json:"rangeDays"`
	Days        []DayPoint `json:"days"`
	ByOptimizer []NameVal  `json:"byOptimizer"`
	ByProvider  []NameVal  `json:"byProvider"`
	TotalTokens int        `json:"totalTokens"`
	TotalSaved  int        `json:"totalSaved"`
	Events      int        `json:"events"`
}

// usageStore appends events to per-month JSONL files under aiworkspace/usage/. Small and dependency-free;
// suitable for local metadata. (Heavy analytics can move to the companion later.)
type usageStore struct {
	dir string
	mu  sync.Mutex
	now func() time.Time
}

func newUsageStore(baseDir string) *usageStore {
	d := filepath.Join(baseDir, "usage")
	_ = os.MkdirAll(d, 0o755)
	return &usageStore{dir: d, now: time.Now}
}

func (u *usageStore) monthPath(t time.Time) string {
	return filepath.Join(u.dir, t.UTC().Format("2006-01")+".jsonl")
}

// record appends one usage event.
func (u *usageStore) record(e UsageEvent) {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now().UTC()
	if e.TS == "" {
		e.TS = now.Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(u.monthPath(now), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// summary reads the current (and, if near the boundary, previous) month files and rolls up today/month
// totals plus a 14-day sparkline. Empty stores return zeros — an honest "no usage yet".
func (u *usageStore) summary() UsageSummary {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now().UTC()
	today := now.Format("2006-01-02")
	month := now.Format("2006-01")

	// 14-day window buckets.
	buckets := map[string]*DayPoint{}
	order := make([]string, 0, 14)
	for i := 13; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		order = append(order, d)
		buckets[d] = &DayPoint{Date: d}
	}

	var s UsageSummary
	// Read this month + previous month (covers the 14-day window across a boundary).
	for _, mp := range []string{u.monthPath(now), u.monthPath(now.AddDate(0, 0, -14))} {
		f, err := os.Open(mp)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var e UsageEvent
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			s.Events++
			tok := e.InputTok + e.OutputTok
			day := ""
			if len(e.TS) >= 10 {
				day = e.TS[:10]
			}
			if len(e.TS) >= 7 && e.TS[:7] == month {
				s.MonthTokens += tok
				s.MonthSaved += e.SavedTok
				s.MonthCost += e.CostEstUSD
				s.MonthSavedU += e.SavedEstUSD
			}
			if day == today {
				s.TodayTokens += tok
				s.TodaySaved += e.SavedTok
			}
			if bp, ok := buckets[day]; ok {
				bp.Tokens += tok
				bp.Saved += e.SavedTok
			}
		}
		f.Close()
		if u.monthPath(now) == u.monthPath(now.AddDate(0, 0, -14)) {
			break // same file, don't read twice
		}
	}
	for _, d := range order {
		s.Days = append(s.Days, *buckets[d])
	}
	return s
}

// series builds the Usage analytics view over the last rangeDays: daily totals + per-optimizer and
// per-provider breakdowns.
func (u *usageStore) series(rangeDays int) UsageSeries {
	u.mu.Lock()
	defer u.mu.Unlock()
	if rangeDays <= 0 {
		rangeDays = 30
	}
	if rangeDays > 365 {
		rangeDays = 365
	}
	now := u.now().UTC()

	buckets := map[string]*DayPoint{}
	order := make([]string, 0, rangeDays)
	months := map[string]bool{}
	for i := rangeDays - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		d := day.Format("2006-01-02")
		order = append(order, d)
		buckets[d] = &DayPoint{Date: d}
		months[u.monthPath(day)] = true
	}
	cutoff := order[0] // earliest day in range (YYYY-MM-DD compares lexically)

	byOpt := map[string]int{}
	byProv := map[string]int{}
	var s UsageSeries
	s.RangeDays = rangeDays

	for mp := range months {
		f, err := os.Open(mp)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var e UsageEvent
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			day := ""
			if len(e.TS) >= 10 {
				day = e.TS[:10]
			}
			if day < cutoff {
				continue
			}
			tok := e.InputTok + e.OutputTok
			s.Events++
			s.TotalTokens += tok
			s.TotalSaved += e.SavedTok
			if bp, ok := buckets[day]; ok {
				bp.Tokens += tok
				bp.Saved += e.SavedTok
			}
			if e.Optimizer != "" {
				byOpt[e.Optimizer] += e.SavedTok
			}
			if e.Provider != "" {
				byProv[e.Provider] += tok
			}
		}
		f.Close()
	}
	for _, d := range order {
		s.Days = append(s.Days, *buckets[d])
	}
	s.ByOptimizer = topNameVals(byOpt, 12)
	s.ByProvider = topNameVals(byProv, 12)
	return s
}

// topNameVals turns a name→value map into a slice sorted by value desc, capped at n.
func topNameVals(m map[string]int, n int) []NameVal {
	out := make([]NameVal, 0, len(m))
	for k, v := range m {
		out = append(out, NameVal{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Name < out[j].Name
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
