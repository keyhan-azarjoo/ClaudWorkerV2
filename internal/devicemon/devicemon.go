// Package devicemon is the ClaudWorker V2 DEVICE LOG MONITOR.
//
// It periodically ("time to time") captures serial logs from connected ESP32 boards,
// analyses them for REAL fault signatures (panic / watchdog-fire / brownout / boot-fail /
// FS-error / heap-critical / reset-loop / TLS+MQTT+WiFi errors), keeps a rolling per-device
// buffer the Operations Console shows, tracks the free-heap trend across cycles to catch
// slow leaks, and invokes an OnBug hook the first time a new fault signature appears on a
// device (the serve layer turns that into a deduped Jira ticket).
//
// The serial read + analysis is delegated to scripts/device_logmon.py (same shell-out
// pattern the V1 dashboard + the Sentry watcher use) so no cgo/serial dependency is added
// to the production binary. "Get ALL the logs": serial is the live local source; cloud
// device logs (Mongo + /ClientLogs/query + Sentry + LAN /diagnostics) are fetched by the
// serve layer — there is NO Azure blob SAS for device logs (only firmware .bin has one).
package devicemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// Device is one board to watch.
type Device struct {
	Name string `json:"name"`
	Port string `json:"port"` // /dev/cu.* serial path
	HwID string `json:"hwId"` // MAC, used for stable Jira dedup labels
}

// Check is one capture cycle's result (mirrors scripts/device_logmon.py JSON output).
type Check struct {
	At            int64            `json:"at"`
	OK            bool             `json:"ok"`
	Booted        bool             `json:"booted"`
	SetupComplete bool             `json:"setup_complete"`
	LittleFsOK    bool             `json:"littlefs_ok"`
	CryptoOK      bool             `json:"crypto_ok"`
	Provisioning  bool             `json:"provisioning"`
	HeapMin       *int             `json:"heap_min"`
	HeapMax       *int             `json:"heap_max"`
	Severity      string           `json:"severity"` // ok | warn | bug
	Findings      []map[string]any `json:"findings"`
	Bytes         int              `json:"bytes"`
	LogTail       string           `json:"log_tail"`
	Error         string           `json:"error,omitempty"`
}

// DevState is the accumulated per-device view.
type DevState struct {
	Name        string           `json:"name"`
	Port        string           `json:"port"`
	HwID        string           `json:"hwId,omitempty"`
	Present     bool             `json:"present"`
	Checks      int              `json:"checks"`
	Last        *Check           `json:"last"`
	History     []*Check         `json:"history"`   // most-recent-last, capped
	HeapTrend   []int            `json:"heapTrend"` // heap_min across cycles (leak detection)
	OpenBugs    map[string]int64 `json:"openBugs"`  // sig -> first-seen unix (already filed)
	LeakFlagged bool             `json:"leakFlagged"`
}

// Bug is the payload handed to OnBug when a NEW fault signature is seen on a device.
type Bug struct {
	Device Device
	Sig    string
	Sample string
	Check  *Check
}

// Monitor holds live per-device state and runs the periodic capture loop.
type Monitor struct {
	script   string
	python   string // python3 interpreter that can import pyserial (default "python3")
	interval time.Duration
	histCap  int
	devices  func() []Device // supplied by serve (resource manager or fixed list)
	onBug    func(Bug)       // serve wires this to Jira

	mu    sync.Mutex
	state map[string]*DevState // key = port
}

// New builds a Monitor. scriptPath = absolute path to scripts/device_logmon.py.
func New(scriptPath string, devices func() []Device, onBug func(Bug)) *Monitor {
	return &Monitor{
		script:   scriptPath,
		python:   "python3",
		interval: 6 * time.Minute,
		histCap:  48,
		devices:  devices,
		onBug:    onBug,
		state:    map[string]*DevState{},
	}
}

// SetInterval overrides the capture cadence (min 60s).
func (m *Monitor) SetInterval(d time.Duration) {
	if d >= time.Minute {
		m.interval = d
	}
}

// SetPython sets the python3 interpreter used to run the serial analyzer (must have pyserial).
func (m *Monitor) SetPython(bin string) {
	if bin != "" {
		m.python = bin
	}
}

// Start launches the periodic loop; it returns immediately and stops when ctx is done.
// Each cycle is panic-guarded so a monitor bug can never crash the control plane.
func (m *Monitor) Start(ctx context.Context) {
	if m.script == "" {
		return
	}
	go func() {
		t := time.NewTimer(25 * time.Second) // let the console come up first
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			func() {
				defer func() { _ = recover() }()
				m.scan(ctx)
			}()
			t.Reset(m.interval)
		}
	}()
}

func (m *Monitor) scan(ctx context.Context) {
	if _, err := os.Stat(m.script); err != nil {
		return
	}
	for _, d := range m.devices() {
		if d.Port == "" {
			continue
		}
		m.CheckOne(ctx, d)
	}
}

// CheckOne captures + analyses one device now (also used by the console "recheck" command).
func (m *Monitor) CheckOne(ctx context.Context, d Device) {
	m.mu.Lock()
	st := m.state[d.Port]
	if st == nil {
		st = &DevState{Name: d.Name, Port: d.Port, HwID: d.HwID, OpenBugs: map[string]int64{}}
		m.state[d.Port] = st
	}
	st.Name, st.HwID = d.Name, d.HwID
	m.mu.Unlock()

	if _, err := os.Stat(d.Port); err != nil {
		m.mu.Lock()
		st.Present = false
		m.mu.Unlock()
		return
	}

	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	py := m.python
	if py == "" {
		py = "python3"
	}
	out, err := exec.CommandContext(cctx, py, m.script, "--port", d.Port, "--name", d.Name, "--secs", "18").Output()
	chk := &Check{At: time.Now().Unix()}
	if err != nil {
		chk.OK, chk.Severity, chk.Error = false, "warn", "capture failed: "+err.Error()
	} else if e := json.Unmarshal(out, chk); e != nil {
		chk.OK, chk.Severity, chk.Error = false, "warn", "bad monitor output"
	}
	chk.At = time.Now().Unix()

	// a boot that never reaches Setup:Complete is itself a bug
	if chk.Booted && !chk.SetupComplete && chk.Severity != "bug" {
		chk.Severity = "bug"
		chk.Findings = append(chk.Findings, map[string]any{"sig": "setup_incomplete", "count": 1,
			"sample": "booted but never reached 'Setup: Complete'"})
	}

	m.mu.Lock()
	st.Present = true
	st.Checks++
	st.Last = chk
	st.History = append(st.History, chk)
	if len(st.History) > m.histCap {
		st.History = st.History[len(st.History)-m.histCap:]
	}
	if chk.HeapMin != nil {
		st.HeapTrend = append(st.HeapTrend, *chk.HeapMin)
		if len(st.HeapTrend) > m.histCap {
			st.HeapTrend = st.HeapTrend[len(st.HeapTrend)-m.histCap:]
		}
	}
	newBugs := map[string]string{}
	if chk.Severity == "bug" {
		for _, f := range chk.Findings {
			sig, _ := f["sig"].(string)
			if sig == "" {
				continue
			}
			if _, seen := st.OpenBugs[sig]; seen {
				continue
			}
			sample, _ := f["sample"].(string)
			newBugs[sig] = sample
			st.OpenBugs[sig] = time.Now().Unix()
		}
	}
	// cross-cycle leak: heap_min trending down >40KB over >=6 checks (fire once)
	leak := false
	if len(st.HeapTrend) >= 6 && !st.LeakFlagged {
		n := len(st.HeapTrend)
		if avg(st.HeapTrend[:2])-avg(st.HeapTrend[n-2:]) > 40000 {
			leak, st.LeakFlagged = true, true
			st.OpenBugs["heap_leak"] = time.Now().Unix()
		}
	}
	trendCopy := append([]int(nil), st.HeapTrend...)
	m.mu.Unlock()

	if m.onBug != nil {
		for sig, sample := range newBugs {
			m.onBug(Bug{Device: d, Sig: sig, Sample: sample, Check: chk})
		}
		if leak {
			m.onBug(Bug{Device: d, Sig: "heap_leak", Check: chk,
				Sample: sampleTrend(trendCopy)})
		}
	}
}

// Snapshot returns a stable, sorted copy of all device states for the console.
func (m *Monitor) Snapshot() []DevState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DevState, 0, len(m.state))
	for _, st := range m.state {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// One returns the full state for one port (nil if unknown).
func (m *Monitor) One(port string) *DevState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.state[port]; st != nil {
		cp := *st
		return &cp
	}
	return nil
}

// Devices exposes the current configured device list (for the console recheck command).
func (m *Monitor) Devices() []Device { return m.devices() }

func avg(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := 0
	for _, x := range xs {
		s += x
	}
	return s / len(xs)
}

func sampleTrend(xs []int) string {
	b, _ := json.Marshal(xs)
	return "free-heap trend " + string(b) + " (declining >40KB across checks)"
}

// SigTitle / SigPriority give human labels + Jira priority for a fault signature.
func SigTitle(sig string) string {
	m := map[string]string{
		"panic": "firmware panic / crash", "watchdog_fire": "task watchdog reset",
		"brownout": "brownout reset (power)", "heap_corrupt": "heap corruption",
		"boot_fail": "no bootable app / bad image", "fs_error": "filesystem mount failed",
		"heap_critical": "critically low free heap (near-OOM)", "reset_loop": "boot/reset loop",
		"heap_leak": "free-heap leak over time", "mbedtls_err": "TLS handshake error",
		"mqtt_fail": "MQTT connect failure", "wifi_fail": "WiFi association failure",
		"setup_incomplete": "boot never completes setup",
	}
	if t := m[sig]; t != "" {
		return t
	}
	return sig
}

func SigPriority(sig string) string {
	switch sig {
	case "mbedtls_err", "mqtt_fail", "wifi_fail":
		return "Medium"
	default:
		return "High"
	}
}
