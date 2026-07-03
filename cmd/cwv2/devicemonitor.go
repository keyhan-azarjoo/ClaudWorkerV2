package main

// Device Log Monitor wiring (Operations Console → "Device Logs").
//
// Starts the internal/devicemon monitor (serial capture + fault analysis via
// scripts/device_logmon.py), turns each NEW fault signature into a deduped Jira Bug,
// and exposes the state to the ops-console SPA via Control-Plane queries/commands:
//
//   GET  /v1/query/devicemonitor.faults    → table rows (one per device)
//   GET  /v1/query/devicemonitor.devices   → full per-device state (history, heap trend)
//   GET  /v1/query/devicemonitor.one?port= → one device's detail (+ recent serial tail)
//   POST /v1/command/devicemonitor.recheck  {"port":"…"}    → capture that device now
//   POST /v1/command/devicemonitor.cloudlogs {"deviceId":…} → cloud logs (Mongo/ClientLogs) note
//
// Fault events publish "DeviceFaultDetected" on the bus so the console refreshes live.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/config"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	"github.com/myotgo/ClaudWorkerV2/internal/devicemon"
	"github.com/myotgo/ClaudWorkerV2/internal/jira"
	"github.com/myotgo/ClaudWorkerV2/internal/secrets"
)

// knownBoards are the MyOTGO C6 test boards (stable WCH-bridge port paths). Any other
// ESP bridge/serial port found on disk is added automatically (the analyzer ignores
// non-ESP ports gracefully — booted=false, no ticket).
var knownBoards = []devicemon.Device{
	{Name: "ESP32-C6 Temperature (Board A)", Port: "/dev/cu.usbmodem5B901441951", HwID: "98a3169cbd18"},
	{Name: "ESP32-C6 Extension (Board B)", Port: "/dev/cu.usbmodem5B901439271", HwID: "acebe6cb57f0"},
}

// StartDeviceMonitor launches the monitor + registers its Control-Plane endpoints.
// Safe to call in any mode; it no-ops cleanly if the analyzer script isn't present.
func StartDeviceMonitor(ctx context.Context, log *slog.Logger, cfg config.Config, cp *controlplane.Server) {
	script := resolveDevmonScript()
	if script == "" {
		log.Warn("device monitor: scripts/device_logmon.py not found; device log monitoring disabled")
		return
	}

	// Best-effort Jira client for auto-filing (nil if creds unavailable → faults still shown, no ticket).
	var jc *jira.Client
	projectKey := "SCRUM"
	if email, token, err := jiraCreds(cfg); err == nil && cfg.Jira.BaseURL != "" && token != "" {
		jc = jira.New(cfg.Jira.BaseURL, email, token)
		if pk := projectKeyFromJQL(cfg.Jira.WorkJQL); pk != "" {
			projectKey = pk
		}
	} else {
		log.Warn("device monitor: Jira not configured; faults will be shown but not ticketed")
	}

	onBug := func(b devicemon.Bug) {
		title := devicemon.SigTitle(b.Sig)
		log.Warn("device fault detected", "device", b.Device.Name, "sig", b.Sig, "sample", b.Sample)
		cp.Bus().Publish("DeviceFaultDetected", "devicemonitor", map[string]any{
			"device": b.Device.Name, "port": b.Device.Port, "sig": b.Sig, "title": title, "at": time.Now().Unix(),
		})
		if jc == nil {
			return
		}
		// Idempotent dedup label survives restarts: only ONE ticket per (device,fault).
		label := devmonLabel(b.Device.HwID, b.Device.Port, b.Sig)
		jql := fmt.Sprintf(`project = %s AND labels = "%s"`, projectKey, label)
		if ex, err := jc.Search(ctx, jql, []string{"key"}, 1); err == nil && len(ex.Issues) > 0 {
			return // already ticketed
		}
		summary := fmt.Sprintf("🔴 [device-monitor] %s: %s", b.Device.Name, title)
		if len([]rune(summary)) > 240 {
			summary = string([]rune(summary)[:240])
		}
		desc := devmonDesc(b)
		in := jira.CreateIssueInput{
			ProjectKey: projectKey, Summary: summary, Description: desc,
			IssueType: "Bug", Priority: devicemon.SigPriority(b.Sig),
			Labels: []string{"device-monitor", "hardware", "auto-detected", label, devmonSlug(b.Device.Name)},
		}
		key, err := jc.CreateIssue(ctx, in)
		if err != nil {
			in.Priority = "" // some projects reject priority on create
			key, err = jc.CreateIssue(ctx, in)
		}
		if err != nil {
			log.Error("device monitor: jira create failed", "error", err.Error())
			return
		}
		log.Info("device monitor: filed Jira bug", "key", key, "device", b.Device.Name, "sig", b.Sig)
	}

	mon := devicemon.New(script, currentDevices, onBug)
	if py := resolveDevmonPython(); py != "" {
		mon.SetPython(py)
	}
	if iv := os.Getenv("CWV2_DEVMON_INTERVAL"); iv != "" {
		if d, err := time.ParseDuration(iv); err == nil {
			mon.SetInterval(d)
		}
	}
	mon.Start(ctx)
	log.Info("device monitor started", "script", script, "python", resolveDevmonPython(), "boards", len(currentDevices()))

	// ---- Control-Plane endpoints ----
	cp.Query("devicemonitor.faults", func(_ context.Context, _ url.Values) (any, error) {
		return devmonFaultRows(mon), nil
	})
	cp.Query("devicemonitor.devices", func(_ context.Context, _ url.Values) (any, error) {
		return mon.Snapshot(), nil
	})
	cp.Query("devicemonitor.one", func(_ context.Context, p url.Values) (any, error) {
		st := mon.One(p.Get("port"))
		if st == nil {
			return nil, fmt.Errorf("no such device port")
		}
		return st, nil
	})
	cp.Command("devicemonitor.recheck", func(cctx context.Context, body []byte) (any, error) {
		var req struct {
			Port string `json:"port"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Port == "" {
			return nil, fmt.Errorf("port is required")
		}
		var dev devicemon.Device
		for _, d := range currentDevices() {
			if d.Port == req.Port {
				dev = d
				break
			}
		}
		if dev.Port == "" {
			dev = devicemon.Device{Name: "ESP32 " + filepath.Base(req.Port), Port: req.Port}
		}
		go mon.CheckOne(context.Background(), dev)
		return map[string]any{"ok": true, "rechecking": req.Port}, nil
	})
	cp.Command("devicemonitor.cloudlogs", func(cctx context.Context, body []byte) (any, error) {
		var req struct {
			DeviceID string `json:"deviceId"`
		}
		_ = json.Unmarshal(body, &req)
		rows, note := fetchCloudDeviceLogs(cctx, cfg, req.DeviceID)
		return map[string]any{"note": note, "rows": rows}, nil
	})
}

// currentDevices = the known C6 boards + any other ESP bridge/serial ports present on disk.
func currentDevices() []devicemon.Device {
	seen := map[string]bool{}
	out := []devicemon.Device{}
	for _, d := range knownBoards {
		out = append(out, d)
		seen[d.Port] = true
	}
	for _, pat := range []string{"/dev/cu.usbmodem5B*", "/dev/cu.usbserial*", "/dev/ttyUSB*", "/dev/ttyACM*"} {
		ports, _ := filepath.Glob(pat)
		for _, p := range ports {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, devicemon.Device{Name: "ESP32 " + filepath.Base(p), Port: p})
		}
	}
	return out
}

// devmonFaultRows flattens monitor state into one table row per device for the console.
func devmonFaultRows(mon *devicemon.Monitor) []map[string]any {
	rows := []map[string]any{}
	for _, st := range mon.Snapshot() {
		level, msg := "ok", "healthy"
		at := ""
		heap := ""
		if st.Checks == 0 {
			level, msg = "pending", "awaiting first capture"
		} else if !st.Present {
			level, msg = "absent", "not connected"
		} else if st.Last != nil {
			level = st.Last.Severity
			at = time.Unix(st.Last.At, 0).Format("15:04:05")
			if st.Last.HeapMin != nil {
				heap = fmt.Sprintf("%dK", *st.Last.HeapMin/1024)
			}
			if len(st.Last.Findings) > 0 {
				parts := []string{}
				for _, f := range st.Last.Findings {
					sig, _ := f["sig"].(string)
					parts = append(parts, devicemon.SigTitle(sig))
				}
				msg = strings.Join(parts, "; ")
			} else if st.Last.Booted {
				msg = "boot OK"
				if st.Last.SetupComplete {
					msg = "boot + setup OK"
				}
			} else {
				msg = "present, no boot output"
			}
		}
		tickets := []string{}
		for sig := range st.OpenBugs {
			tickets = append(tickets, devicemon.SigTitle(sig))
		}
		rows = append(rows, map[string]any{
			"device": st.Name, "port": st.Port, "level": level, "message": msg,
			"heap": heap, "checks": st.Checks, "ticket": strings.Join(tickets, ", "), "at": at,
		})
	}
	return rows
}

func devmonDesc(b devicemon.Bug) string {
	c := b.Check
	lines := []string{
		"Auto-detected by the ClaudWorker device log monitor on " + time.Now().Format("2006-01-02 15:04 MST") + ".",
		"",
		"Device: " + b.Device.Name,
		"Port: " + b.Device.Port,
		"HW id: " + b.Device.HwID,
		"Fault: " + b.Sig + " — " + devicemon.SigTitle(b.Sig),
		"Detail: " + b.Sample,
	}
	if c != nil {
		lines = append(lines, fmt.Sprintf("Boot=%v Setup=%v LittleFS=%v Crypto=%v", c.Booted, c.SetupComplete, c.LittleFsOK, c.CryptoOK))
		tail := c.LogTail
		if len(tail) > 1500 {
			tail = tail[len(tail)-1500:]
		}
		lines = append(lines, "", "Serial tail:", tail)
	}
	return strings.Join(lines, "\n")
}

// fetchCloudDeviceLogs pulls a device's cloud logs from the backend /ClientLogs/query
// (support-authed). Device logs are NOT in Azure blob (no SAS — only firmware .bin has one);
// the cloud source is Mongo via this API once the device is provisioned + reporting.
func fetchCloudDeviceLogs(ctx context.Context, cfg config.Config, deviceID string) (any, string) {
	r := secrets.NewResolver()
	base, _ := r.Resolve("support-ai-backend-url")
	if base == "" {
		base, _ = r.Resolve("backend-url")
	}
	tok, _ := r.Resolve("support-query-token")
	if base == "" || tok == "" {
		return nil, "cloud logs unavailable: device logs live in Mongo + /ClientLogs/query (support-authed) and Sentry — there is NO Azure blob SAS for device logs (only firmware .bin download has a SAS). Configure support-ai-backend-url + support-query-token to enable."
	}
	if deviceID == "" {
		return nil, "pass deviceId — cloud logs only exist once the device is account-bound + reporting"
	}
	u := strings.TrimRight(base, "/") + "/ClientLogs/query?deviceId=" + url.QueryEscape(deviceID) + "&limit=200"
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, "cloud query failed: " + err.Error()
	}
	defer resp.Body.Close()
	var parsed any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	if resp.StatusCode >= 300 {
		return parsed, fmt.Sprintf("cloud query HTTP %d", resp.StatusCode)
	}
	return parsed, "ok"
}

// resolveDevmonScript finds scripts/device_logmon.py across the live + repo layouts.
func resolveDevmonScript() string {
	cands := []string{}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "scripts", "device_logmon.py"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, ".cw-live", "scripts", "device_logmon.py"))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "scripts", "device_logmon.py"))
	}
	cands = append(cands, "/Volumes/Extreme SSD/ClaudWorkerV2/scripts/device_logmon.py")
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// resolveDevmonPython finds a python3 that can `import serial` (pyserial). Prefers the isolated
// ~/.cw-live/venv, then the Homebrew/Framework interpreters, then bare python3. Returns "" if none
// works (the analyzer then reports pyserial-missing per device instead of silently failing).
func resolveDevmonPython() string {
	cands := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, ".cw-live", "venv", "bin", "python3"))
	}
	cands = append(cands,
		"/Library/Frameworks/Python.framework/Versions/Current/bin/python3",
		"/Library/Frameworks/Python.framework/Versions/3.12/bin/python3",
		"/opt/homebrew/bin/python3",
		"python3",
	)
	for _, c := range cands {
		if c != "python3" {
			if _, err := os.Stat(c); err != nil {
				continue
			}
		}
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := exec.CommandContext(cctx, c, "-c", "import serial").Run()
		cancel()
		if err == nil {
			return c
		}
	}
	return ""
}

func devmonLabel(hwid, port, sig string) string {
	id := hwid
	if id == "" {
		id = filepath.Base(port)
	}
	return "devmon:" + devmonSlug(id) + ":" + devmonSlug(sig)
}

func devmonSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == ':', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
