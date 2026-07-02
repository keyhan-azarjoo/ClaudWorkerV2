// Package discovery implements real Resource Discovery (Phase B #1). Each discoverer implements the
// existing resource.Discoverer interface and feeds resource.Manager.Discover — no duplicated logic:
// the Manager still owns reconciliation, health, availability, pacing, cooldown, scheduling, failover.
//
// Probes are behind injectable seams (CmdRunner / HTTPGetter) so discovery is deterministically
// testable without real hardware, and Simulation Mode needs none of it. A discoverer that finds
// nothing (tool missing / endpoint down) simply returns no resources — never an error that stalls the
// loop.
package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/resource"
)

// CmdRunner runs a command and returns combined output (injectable for tests).
type CmdRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// HTTPGetter GETs a URL and returns the body (injectable for tests).
type HTTPGetter func(ctx context.Context, url string) ([]byte, error)

// DefaultRunner execs the real command with a short timeout.
func DefaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return exec.CommandContext(c, name, args...).Output()
}

// DefaultGetter performs a real HTTP GET with a short timeout.
func DefaultGetter(ctx context.Context, url string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil || len(buf) > 1<<20 {
			break
		}
	}
	return buf, nil
}

// --- Accounts (Claude / Codex) from their config dirs ---

// Accounts discovers provider accounts from config directories. Health = the config dir exists
// (credentials live there; the value is never read). id → configDir.
type Accounts struct {
	Kind resource.Kind     // KindClaudeAccount or KindCodexAccount
	Dirs map[string]string // account id → config dir
	Stat func(path string) bool
}

func (a Accounts) stat(p string) bool {
	if a.Stat != nil {
		return a.Stat(p)
	}
	_, err := os.Stat(p)
	return err == nil
}

func (a Accounts) Discover() ([]resource.Resource, error) {
	var out []resource.Resource
	for id, dir := range a.Dirs {
		engine := "claude"
		if a.Kind == resource.KindCodexAccount {
			engine = "codex"
		}
		health := resource.HealthDown
		if dir == "" || a.stat(dir) {
			health = resource.HealthHealthy
		}
		out = append(out, resource.Resource{
			ID: "acct-" + slug(id), Kind: a.Kind, Name: id, Health: health,
			Labels: map[string]string{"engine": engine, "claude_config_dir": dir, "source": "discovery"},
		})
	}
	return out, nil
}

// --- Local model providers (Ollama / LM Studio / vLLM / OpenAI-compatible) ---

// Provider discovers a local OpenAI-compatible / Ollama endpoint. If reachable it is a healthy
// local_runtime resource with its models listed; if unreachable it yields nothing.
type Provider struct {
	ID      string // "ollama" | "lmstudio" | "vllm" | custom
	BaseURL string // e.g. http://127.0.0.1:11434
	Ollama  bool   // true → /api/tags shape; false → /v1/models shape
	Get     HTTPGetter
}

func (p Provider) Discover() ([]resource.Resource, error) {
	get := p.Get
	if get == nil {
		get = DefaultGetter
	}
	path, key := "/v1/models", "data"
	if p.Ollama {
		path, key = "/api/tags", "models"
	}
	body, err := get(context.Background(), strings.TrimRight(p.BaseURL, "/")+path)
	if err != nil {
		return nil, nil // unreachable → not discovered (not an error)
	}
	models := parseModels(body, key)
	return []resource.Resource{{
		ID: "runtime-" + slug(p.ID), Kind: resource.KindLocalRuntime, Name: p.ID, Health: resource.HealthHealthy,
		Labels: map[string]string{"provider": p.ID, "base_url": p.BaseURL, "models": strings.Join(models, ","), "source": "discovery"},
	}}, nil
}

func parseModels(body []byte, key string) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	var arr []map[string]any
	if json.Unmarshal(m[key], &arr) != nil {
		return nil
	}
	var out []string
	for _, e := range arr {
		for _, k := range []string{"name", "id", "model"} {
			if v, ok := e[k].(string); ok && v != "" {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// --- Devices: Android (adb), iOS simulators (simctl), ESP32 (serial) ---

// Adb discovers Android devices via `adb devices`.
type Adb struct{ Run CmdRunner }

func (a Adb) Discover() ([]resource.Resource, error) {
	run := a.Run
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(context.Background(), "adb", "devices")
	if err != nil {
		return nil, nil
	}
	var res []resource.Resource
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "device" {
			res = append(res, resource.Resource{
				ID: "dev-android-" + slug(f[0]), Kind: resource.KindAndroidDevice, Name: "Android " + f[0],
				Health: resource.HealthHealthy, Labels: map[string]string{"serial": f[0], "reach": "adb", "source": "discovery"},
			})
		}
	}
	return res, nil
}

// Simctl discovers booted iOS simulators via `xcrun simctl list devices booted`.
type Simctl struct{ Run CmdRunner }

func (s Simctl) Discover() ([]resource.Resource, error) {
	run := s.Run
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(context.Background(), "xcrun", "simctl", "list", "devices", "booted")
	if err != nil {
		return nil, nil
	}
	var res []resource.Resource
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "(Booted)") {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(line, "(", 2)[0])
		res = append(res, resource.Resource{
			ID: "dev-iossim-" + slug(name), Kind: resource.KindIPhone, Name: name, Health: resource.HealthHealthy,
			Labels: map[string]string{"reach": "simctl", "simulator": "true", "source": "discovery"},
		})
	}
	return res, nil
}

// Serial discovers ESP32 boards from serial-port device files.
type Serial struct {
	Glob func(pattern string) ([]string, error)
}

func (s Serial) Discover() ([]resource.Resource, error) {
	glob := s.Glob
	if glob == nil {
		glob = filepath.Glob
	}
	var ports []string
	for _, pat := range []string{"/dev/cu.usbserial*", "/dev/cu.usbmodem*", "/dev/ttyUSB*", "/dev/ttyACM*"} {
		if m, err := glob(pat); err == nil {
			ports = append(ports, m...)
		}
	}
	var res []resource.Resource
	for _, p := range ports {
		res = append(res, resource.Resource{
			ID: "dev-esp32-" + slug(filepath.Base(p)), Kind: resource.KindESP32, Name: "ESP32 " + filepath.Base(p),
			Health: resource.HealthHealthy, Labels: map[string]string{"reach": p, "source": "discovery"},
		})
	}
	return res, nil
}

// --- Static (declared) resources: Mac Mini, DGX, Windows build server, Raspberry Pi, etc. ---

// Static yields a fixed set of declared resources (from config / migration). Reachability is not
// probed here (declared infra); the Resource Manager's health monitoring updates it.
type Static struct{ Resources []resource.Resource }

func (s Static) Discover() ([]resource.Resource, error) {
	return append([]resource.Resource(nil), s.Resources...), nil
}

// --- Composite ---

// Composite runs several discoverers and merges their results, de-duplicated by ID. A failing
// discoverer is skipped (its error is swallowed) so one broken probe never stalls discovery.
type Composite []resource.Discoverer

func (c Composite) Discover() ([]resource.Resource, error) {
	seen := map[string]bool{}
	var out []resource.Resource
	for _, d := range c {
		rs, err := d.Discover()
		if err != nil {
			continue
		}
		for _, r := range rs {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out, nil
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
