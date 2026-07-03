package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"claudworker/internal/resource"
)

const fixture = "testdata/v1"

func migrateFixture(t *testing.T) Result {
	t.Helper()
	d, err := Read(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return Migrate(d)
}

func TestAccountsMigrated(t *testing.T) {
	r := migrateFixture(t)
	var claude, codex *resource.Resource
	for i := range r.Resources {
		switch r.Resources[i].Kind {
		case resource.KindClaudeAccount:
			claude = &r.Resources[i]
		case resource.KindCodexAccount:
			codex = &r.Resources[i]
		}
	}
	if claude == nil || codex == nil {
		t.Fatalf("expected a claude + codex account resource, got %+v", r.Resources)
	}
	if claude.Labels["claude_config_dir"] == "" || claude.Labels["engine"] != "claude" {
		t.Errorf("claude account labels = %+v", claude.Labels)
	}
	if codex.Labels["engine"] != "codex" {
		t.Errorf("codex engine label = %+v", codex.Labels)
	}
}

func TestDevicesMigratedAndModemSkipped(t *testing.T) {
	r := migrateFixture(t)
	kinds := map[resource.Kind]bool{}
	for _, res := range r.Resources {
		kinds[res.Kind] = true
		if strings.Contains(strings.ToLower(res.Name), "modem") {
			t.Errorf("modem should be skipped, got %+v", res)
		}
	}
	for _, want := range []resource.Kind{resource.KindAndroidDevice, resource.KindIPhone, resource.KindESP32, resource.KindBuildMachine} {
		if !kinds[want] {
			t.Errorf("missing migrated device kind %q", want)
		}
	}
}

func TestUsageGuardAndConfigMapped(t *testing.T) {
	r := migrateFixture(t)
	if r.Config.UsageGuard.PausePct != 98 || r.Config.UsageGuard.ResumePct != 95 {
		t.Errorf("usage guard = %+v", r.Config.UsageGuard)
	}
	if r.Config.Workflow.MaxConcurrent != 10 || r.Config.Defaults.Model != "claude-opus-4-8" {
		t.Errorf("config = %+v", r.Config)
	}
	if len(r.Config.GateLabels.Required) != 2 || len(r.Config.GateLabels.Blocking) != 2 {
		t.Errorf("gate labels = %+v", r.Config.GateLabels)
	}
}

// TestNoSecretValuesLeak is the hard safety guard: no token/secret VALUE from V1 appears anywhere in
// the migration output (resources, config, matrix). Only references (config dirs) are allowed.
func TestNoSecretValuesLeak(t *testing.T) {
	r := migrateFixture(t)
	blob, _ := json.Marshal(r)
	all := string(blob) + RenderMatrix(r.Matrix) + renderYAML(r.Config)
	for _, secret := range []string{"SUPERSECRET_TOKEN_VALUE", "ANOTHER_SECRET"} {
		if strings.Contains(all, secret) {
			t.Fatalf("secret value %q leaked into migration output", secret)
		}
	}
}

func TestJobsRetiredAndTransientSkipped(t *testing.T) {
	r := migrateFixture(t)
	var jobs, transient *MatrixRow
	for i := range r.Matrix {
		if strings.Contains(r.Matrix[i].Category, "RETIRED") {
			jobs = &r.Matrix[i]
		}
		if strings.Contains(r.Matrix[i].Category, "Transient") {
			transient = &r.Matrix[i]
		}
	}
	if jobs == nil || !strings.Contains(strings.ToLower(jobs.Notes), "issue-driven") {
		t.Errorf("jobs not documented as retired: %+v", jobs)
	}
	if transient == nil || transient.Imported != "0" {
		t.Errorf("transient state not marked skipped: %+v", transient)
	}
}

func TestMatrixCoversAllCategories(t *testing.T) {
	r := migrateFixture(t)
	got := map[string]bool{}
	for _, row := range r.Matrix {
		got[row.Category] = true
		// every row must have a validation OR notes — nothing silently ignored
		if row.Found == "" && row.Imported == "" && row.Skipped == "" && row.Missing == "" {
			t.Errorf("empty matrix row: %+v", row)
		}
	}
	needles := []string{"Accounts", "Devices", "usage guard", "Jira", "Git", "Policies", "Verification", "Discovery", "Console", "Secrets", "SSH", "Notifications", "Logging", "RETIRED", "Transient"}
	for _, n := range needles {
		found := false
		for cat := range got {
			if strings.Contains(cat, n) {
				found = true
			}
		}
		if !found {
			t.Errorf("matrix missing a category matching %q", n)
		}
	}
}

func TestIdempotentDeterministic(t *testing.T) {
	a := migrateFixture(t)
	b := migrateFixture(t)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Migrate is not deterministic")
	}
	// Write twice → byte-identical files (idempotent).
	d1, d2 := t.TempDir(), t.TempDir()
	if err := Write(a, d1); err != nil {
		t.Fatal(err)
	}
	if err := Write(b, d2); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"resources.json", "migrated.yaml", "migration-matrix.json", "migration-matrix.md"} {
		x, _ := os.ReadFile(filepath.Join(d1, f))
		y, _ := os.ReadFile(filepath.Join(d2, f))
		if string(x) != string(y) || len(x) == 0 {
			t.Errorf("%s not idempotent/identical", f)
		}
	}
}

// TestReadIsTolerant proves a missing V1 (empty dir) yields a matrix, not a crash — read-only + safe.
func TestReadTolerantOfMissing(t *testing.T) {
	d, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := Migrate(d)
	if len(r.Matrix) == 0 {
		t.Error("expected a matrix even with no V1 data")
	}
	if len(r.Resources) != 0 {
		t.Errorf("no resources expected from empty V1, got %d", len(r.Resources))
	}
}
