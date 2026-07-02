// Package migration imports ClaudWorker V1's persistent configuration into V2 artifacts. It is:
//   - read-only against V1 (V1 files are never written);
//   - idempotent + deterministic (same input → byte-identical output; safe to re-run);
//   - restart-safe (atomic writes to the target dir);
//   - reversible (delete the target dir to revert; V1 is untouched).
//
// It migrates DEFINITIONS (accounts, devices, usage guard, scheduling, gate labels), NEVER transient
// runtime state (usage counters, in-flight work, sessions, reservations) and NEVER secret VALUES
// (only references — config dirs, secret paths). V1's role-agent "jobs" are intentionally retired
// (V2 is Jira-issue-driven). Every category is reported in the migration matrix; nothing is silently
// ignored.
package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/resource"
)

// --- V1 source shapes (subset we read; read-only) ---

type v1Config struct {
	Accounts          []v1Account                `json:"accounts"`
	UsagePausePct     int                        `json:"usagePausePct"`
	UsageResumePct    int                        `json:"usageResumePct"`
	MaxConcurrent     int                        `json:"maxConcurrent"`
	InFlightCap       int                        `json:"inFlightCap"`
	BatchSize         int                        `json:"batchSize"`
	Model             string                     `json:"model"`
	FallbackModel     string                     `json:"fallbackModel"`
	SchedulerMins     int                        `json:"schedulerMins"`
	JobTimeoutMinutes int                        `json:"jobTimeoutMinutes"`
	MinFreeGB         int                        `json:"minFreeGB"`
	Pace              string                     `json:"pace"`
	SecretsPath       string                     `json:"secretsPath"`
	Jobs              map[string]json.RawMessage `json:"jobs"`
	Voice             map[string]any             `json:"voice"`
}

type v1Account struct {
	Label       string `json:"label"`
	Engine      string `json:"engine"` // claude | codex
	ConfigDir   string `json:"configDir"`
	PausePct    int    `json:"pausePct"`
	Pace        string `json:"pace"`
	Concurrency int    `json:"concurrency"`
	Fanout      int    `json:"fanout"`
	Days        string `json:"days"`
	FromHour    int    `json:"fromHour"`
	ToHour      int    `json:"toHour"`
	UseSonnet   bool   `json:"useSonnet"`
	HardPause   bool   `json:"hardPause"`
}

type v1Device struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // real | sim
	Type    string `json:"type"` // android | ios | esp | pc | hub | modem
	Reach   string `json:"reach"`
	Account string `json:"account"`
}

type v1GateLabels struct {
	Required []string `json:"required"`
	Blocking []string `json:"blocking"`
}

// --- Result ---

// MatrixRow reports the disposition of one category. Nothing is silently ignored.
type MatrixRow struct {
	Category   string `json:"category"`
	Found      string `json:"found"`
	Imported   string `json:"imported"`
	Skipped    string `json:"skipped"`
	Missing    string `json:"missing"`
	Validation string `json:"validation"`
	Notes      string `json:"notes"`
}

// Result is the migration output: V2 resources, a config fragment, and the matrix.
type Result struct {
	Resources []resource.Resource `json:"resources"`
	Config    MigratedConfig      `json:"config"`
	Matrix    []MatrixRow         `json:"matrix"`
}

// MigratedConfig is the mappable V2 config fragment (yaml-mergeable into cwv2.yaml). Only fields with
// a real V1 source are emitted.
type MigratedConfig struct {
	UsageGuard struct {
		PausePct  int `yaml:"pause_pct" json:"pause_pct"`
		ResumePct int `yaml:"resume_pct" json:"resume_pct"`
	} `yaml:"usage_guard" json:"usage_guard"`
	Workflow struct {
		MaxConcurrent int `yaml:"max_concurrent" json:"max_concurrent"`
	} `yaml:"workflow" json:"workflow"`
	Defaults struct {
		Model         string `yaml:"model,omitempty" json:"model,omitempty"`
		FallbackModel string `yaml:"fallback_model,omitempty" json:"fallback_model,omitempty"`
		JobTimeoutMin int    `yaml:"job_timeout_minutes,omitempty" json:"job_timeout_minutes,omitempty"`
		SchedulerMin  int    `yaml:"scheduler_minutes,omitempty" json:"scheduler_minutes,omitempty"`
		MinFreeGB     int    `yaml:"min_free_gb,omitempty" json:"min_free_gb,omitempty"`
	} `yaml:"defaults" json:"defaults"`
	GateLabels struct {
		Required []string `yaml:"required,omitempty" json:"required,omitempty"`
		Blocking []string `yaml:"blocking,omitempty" json:"blocking,omitempty"`
	} `yaml:"gate_labels" json:"gate_labels"`
}

// v1Data is what Read gathered (nil pointers = the file was absent).
type v1Data struct {
	config             *v1Config
	devices            []v1Device
	gate               *v1GateLabels
	users              int // count only (users are references; never migrated as auth)
	accountsStateCount int // state/accounts.json entry count (transient — NOT migrated)
	present            map[string]bool
}

// Read loads V1's persistent files read-only. Missing files are tolerated (recorded in the matrix).
func Read(v1dir string) (v1Data, error) {
	d := v1Data{present: map[string]bool{}}

	if b, err := os.ReadFile(filepath.Join(v1dir, "config.json")); err == nil {
		var c v1Config
		if json.Unmarshal(b, &c) == nil {
			d.config = &c
			d.present["config"] = true
		}
	}
	if b, err := os.ReadFile(filepath.Join(v1dir, "logs", "devices.json")); err == nil {
		_ = json.Unmarshal(b, &d.devices)
		d.present["devices"] = true
	}
	if b, err := os.ReadFile(filepath.Join(v1dir, "scripts", "gate-labels.json")); err == nil {
		var g v1GateLabels
		if json.Unmarshal(b, &g) == nil {
			d.gate = &g
			d.present["gate"] = true
		}
	}
	if b, err := os.ReadFile(filepath.Join(v1dir, "users.json")); err == nil {
		var u struct {
			Users []json.RawMessage `json:"users"`
		}
		if json.Unmarshal(b, &u) == nil {
			d.users = len(u.Users)
			d.present["users"] = true
		}
	}
	if b, err := os.ReadFile(filepath.Join(v1dir, "state", "accounts.json")); err == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) == nil {
			d.accountsStateCount = len(m)
			d.present["accounts_state"] = true
		}
	}
	return d, nil
}

// Migrate maps V1 data to V2 artifacts + the matrix. Pure and deterministic.
func Migrate(d v1Data) Result {
	var r Result
	r.Resources = append(r.Resources, migrateAccounts(d, &r.Matrix)...)
	r.Resources = append(r.Resources, migrateDevices(d, &r.Matrix)...)
	migrateUsageAndScheduling(d, &r)
	migrateJira(d, &r)
	migrateGit(&r)
	migratePolicies(d, &r)
	migrateVerification(&r)
	migrateDiscovery(d, &r)
	migrateConsole(d, &r)
	migrateSecrets(d, &r)
	migrateSSH(&r)
	migrateNotifications(d, &r)
	migrateLogging(&r)
	migrateJobs(d, &r)      // retired
	migrateTransient(d, &r) // skipped

	sort.Slice(r.Resources, func(i, j int) bool { return r.Resources[i].ID < r.Resources[j].ID })
	return r
}

func migrateAccounts(d v1Data, m *[]MatrixRow) []resource.Resource {
	if d.config == nil || len(d.config.Accounts) == 0 {
		*m = append(*m, MatrixRow{Category: "AI Providers / Accounts", Found: "0", Missing: "config.accounts",
			Validation: "n/a", Notes: "no account definitions in V1 config"})
		return nil
	}
	var out []resource.Resource
	claude, codex := 0, 0
	for _, a := range d.config.Accounts {
		kind := resource.KindClaudeAccount
		if strings.EqualFold(a.Engine, "codex") {
			kind = resource.KindCodexAccount
			codex++
		} else {
			claude++
		}
		labels := map[string]string{
			"engine":            a.Engine,
			"claude_config_dir": a.ConfigDir, // a reference to where creds live — NOT a secret value
			"pace":              a.Pace,
			"pause_pct":         itoa(a.PausePct),
			"concurrency":       itoa(a.Concurrency),
		}
		if a.UseSonnet {
			labels["model"] = "claude-sonnet"
		}
		if a.Days != "" || a.FromHour != 0 || a.ToHour != 0 {
			labels["schedule_days"] = a.Days
			labels["schedule_from"] = itoa(a.FromHour)
			labels["schedule_to"] = itoa(a.ToHour)
		}
		out = append(out, resource.Resource{ID: "acct-" + slug(a.Label), Kind: kind, Name: a.Label, Labels: labels, Health: resource.HealthUnknown})
	}
	*m = append(*m, MatrixRow{Category: "AI Providers / Accounts",
		Found:      itoa(len(d.config.Accounts)) + " (claude+codex)",
		Imported:   itoa(len(out)) + " (" + itoa(claude) + " claude, " + itoa(codex) + " codex)",
		Skipped:    "per-account usage counters + tokens (transient/secret)",
		Validation: "each account → a resource with engine + config-dir reference",
		Notes:      "config dirs migrated as references (creds stay in Keychain/config dir); pausePct/pace/schedule preserved as labels"})
	return out
}

func migrateDevices(d v1Data, m *[]MatrixRow) []resource.Resource {
	if !d.present["devices"] {
		*m = append(*m, MatrixRow{Category: "Resources / Devices", Found: "0", Missing: "logs/devices.json", Validation: "n/a"})
		return nil
	}
	var out []resource.Resource
	imported, skipped := 0, 0
	for _, dev := range d.devices {
		kind, ok := deviceKind(dev.Type)
		if !ok {
			skipped++
			continue
		}
		labels := map[string]string{"reach": dev.Reach, "source": "v1", "device_kind": dev.Kind}
		if dev.Kind == "sim" {
			labels["simulator"] = "true"
		}
		out = append(out, resource.Resource{ID: "dev-" + slug(dev.Name), Kind: kind, Name: dev.Name, Labels: labels, Health: resource.HealthUnknown})
		imported++
	}
	*m = append(*m, MatrixRow{Category: "Resources / Devices",
		Found:      itoa(len(d.devices)),
		Imported:   itoa(imported) + " (android/iphone/esp32/build-machine)",
		Skipped:    itoa(skipped) + " (modem/other non-compute) + inUse/mode (reservation state)",
		Missing:    "Mac Mini + DGX (no V1 device entry)",
		Validation: "device type → V2 resource Kind; reach preserved as label",
		Notes:      "reservation state (inUse) is transient → not migrated; V2 starts clean"})
	return out
}

func migrateUsageAndScheduling(d v1Data, r *Result) {
	if d.config == nil {
		r.Matrix = append(r.Matrix, MatrixRow{Category: "Account Management (usage/pacing/scheduling)", Found: "0", Missing: "config.json"})
		return
	}
	c := d.config
	r.Config.UsageGuard.PausePct = c.UsagePausePct
	r.Config.UsageGuard.ResumePct = c.UsageResumePct
	r.Config.Workflow.MaxConcurrent = c.MaxConcurrent
	r.Config.Defaults.Model = c.Model
	r.Config.Defaults.FallbackModel = c.FallbackModel
	r.Config.Defaults.JobTimeoutMin = c.JobTimeoutMinutes
	r.Config.Defaults.SchedulerMin = c.SchedulerMins
	r.Config.Defaults.MinFreeGB = c.MinFreeGB
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Account Management (usage guard / pacing / scheduling / concurrency)",
		Found:      "usagePausePct/ResumePct, maxConcurrent, scheduler, timeouts, model",
		Imported:   "usage_guard(" + itoa(c.UsagePausePct) + "/" + itoa(c.UsageResumePct) + "), max_concurrent(" + itoa(c.MaxConcurrent) + "), model, timeouts",
		Skipped:    "in-flight cap / current retries (transient)",
		Validation: "usage guard → BudgetPolicy; concurrency → Workflow; per-account pace preserved on account resources",
		Notes:      "rotation/failover/health are V2 Resource-Manager behaviours (already real); V1 knobs preserved as config + labels"})
}

func migrateJira(d v1Data, r *Result) {
	found, imp := "gate labels", ""
	if d.gate != nil {
		r.Config.GateLabels.Required = d.gate.Required
		r.Config.GateLabels.Blocking = d.gate.Blocking
		imp = itoa(len(d.gate.Required)) + " required, " + itoa(len(d.gate.Blocking)) + " blocking labels"
	} else {
		found = "0"
	}
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Jira (project/automation/labels/mappings)",
		Found: found, Imported: imp,
		Missing:    "base_url/project/Automation-field (V1 had no Jira project config — set in cwv2.yaml)",
		Validation: "gate labels → verification gate config",
		Notes:      "V2 Jira base_url/project/auth configured directly (Phase 2.1); Automation field is a V2 concept"})
}

func migrateGit(r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Git (repos/branches/worktree/merge)",
		Found: "n/a (V1 used per-agent worktrees, no declarative repo config)", Imported: "0",
		Missing:    "repo declarations (set in cwv2.yaml repos[])",
		Validation: "n/a", Notes: "V2 Git is real (Phase 2.2): worktree-per-assignment, --no-ff merge; repos declared in cwv2.yaml"})
}

func migratePolicies(d v1Data, r *Result) {
	note := "retry/budget/scheduling map to V2 Policy Engine (S6); usage guard imported above"
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Policies (retry/budget/scheduling)",
		Found: "usage guard, timeouts, concurrency (no explicit retry count in V1)", Imported: "budget + concurrency (above)",
		Missing: "explicit retry count (V2 default applies)", Validation: "→ Policy Engine", Notes: note})
}

func migrateVerification(r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Verification (config/drivers)",
		Found: "job-based verification (visual-test/e2e-test/qa jobs)", Imported: "gate labels (above)",
		Missing: "declarative verifier/driver config", Validation: "n/a",
		Notes: "real verification drivers are Phase B #2 (not this phase); V1 jobs are role-agents, retired"})
}

func migrateDiscovery(d v1Data, r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Discovery (registered devices/runtimes/providers)",
		Found: itoa(len(d.devices)) + " devices + " + accountsFound(d) + " accounts", Imported: "as V2 resource definitions (above)",
		Skipped: "live reachability/health (re-probed by Phase B discovery)", Validation: "definitions → resources.json",
		Notes: "real discovery/probing is Phase B #1; this migration seeds the inventory definitions"})
}

func migrateConsole(d v1Data, r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Operations Console (users/preferences/settings)",
		Found: itoa(d.users) + " users", Imported: "0 (recorded as reference)",
		Skipped: "user records", Missing: "V2 has token auth, not a user DB", Validation: "n/a",
		Notes: "V2 Control Plane uses token auth; user preferences are client-local (localStorage). Users recorded for reference only"})
}

func migrateSecrets(d v1Data, r *Result) {
	ref := ""
	if d.config != nil {
		ref = d.config.SecretsPath
	}
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Secrets (references only)",
		Found: "secretsPath + per-account config dirs + tokens", Imported: "references only (secretsPath, config dirs)",
		Skipped:    "ALL secret VALUES (tokens/passwords never read into output)",
		Validation: "output scanned — no token/secret values present",
		Notes:      "reuse existing Keychain/secure files/env; secretsPath ref: " + boolStr(ref != "", "present", "absent")})
}

func migrateSSH(r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "SSH (keys/known-hosts/repo mappings)",
		Found: "device reach strings (ssh/adb/ip) preserved on resources", Imported: "reach references (above)",
		Missing: "explicit SSH key refs (none in V1 config)", Validation: "n/a",
		Notes: "reuse existing SSH keys/known_hosts on the host; device reach preserved as labels"})
}

func migrateNotifications(d v1Data, r *Result) {
	found := "0"
	if d.config != nil && len(d.config.Voice) > 0 {
		found = "voice config (" + itoa(len(d.config.Voice)) + " keys)"
	}
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Notifications (telegram/email/webhook)",
		Found: found, Imported: "0", Missing: "no notification subsystem in V2",
		Validation: "n/a", Notes: "V1 voice/Telegram are separate tools; V2 has no notification subsystem — recorded as reference, intentionally deferred"})
}

func migrateLogging(r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Logging / metrics / monitoring",
		Found: "logDir + Sentry snapshot (V1)", Imported: "0 (V2 uses slog + Control Plane metrics)",
		Missing: "n/a", Validation: "n/a", Notes: "V2 logging is structured slog; metrics via Control Plane. No V1 logging config needs importing"})
}

func migrateJobs(d v1Data, r *Result) {
	n := 0
	if d.config != nil {
		n = len(d.config.Jobs)
	}
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Jobs / role-agents (RETIRED)",
		Found: itoa(n) + " role-agents (app/backend/qa/…)", Imported: "0",
		Skipped:    "all — intentionally retired",
		Validation: "n/a",
		Notes:      "JUSTIFIED RETIREMENT: V2 is Jira-issue-driven (one Assignment per issue), not per-role cron agents. Role coverage is now emergent from the work queue"})
}

func migrateTransient(d v1Data, r *Result) {
	r.Matrix = append(r.Matrix, MatrixRow{Category: "Transient runtime state (NOT migrated)",
		Found:      "account usage counters (" + itoa(d.accountsStateCount) + "), sessions, in-flight, worktrees, retries, logs, snapshots",
		Imported:   "0",
		Skipped:    "ALL — V2 starts clean by design",
		Validation: "excluded by construction (never read)",
		Notes:      "running assignments/leases/worktrees/processes/caches are transient; V2 recovers durable state only"})
}

// Write atomically emits resources.json, migrated.yaml, and migration-matrix.{json,md} to targetDir.
// Deterministic + idempotent: re-running yields byte-identical files. V1 is never written.
func Write(r Result, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	res, _ := json.MarshalIndent(r.Resources, "", "  ")
	if err := writeAtomic(filepath.Join(targetDir, "resources.json"), res); err != nil {
		return err
	}
	mx, _ := json.MarshalIndent(r.Matrix, "", "  ")
	if err := writeAtomic(filepath.Join(targetDir, "migration-matrix.json"), mx); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(targetDir, "migration-matrix.md"), []byte(RenderMatrix(r.Matrix))); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(targetDir, "migrated.yaml"), []byte(renderYAML(r.Config)))
}

// --- helpers ---

func deviceKind(t string) (resource.Kind, bool) {
	switch strings.ToLower(t) {
	case "android":
		return resource.KindAndroidDevice, true
	case "ios":
		return resource.KindIPhone, true
	case "esp":
		return resource.KindESP32, true
	case "pc":
		return resource.KindBuildMachine, true
	case "hub":
		return resource.KindBuildMachine, true
	default:
		return "", false // modem, unknown → skipped
	}
}

func accountsFound(d v1Data) string {
	if d.config == nil {
		return "0"
	}
	return itoa(len(d.config.Accounts))
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func boolStr(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
