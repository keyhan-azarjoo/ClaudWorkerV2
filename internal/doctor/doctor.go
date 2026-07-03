// Package doctor runs deterministic preflight checks (docs/14_Deployment.md `cwv2 doctor`).
//
// It validates config, verifies the engine home is writable, confirms required tools are present,
// reports secret resolvability (by name only — no value is ever read/logged, NFR-6), and reports
// per-plugin toolchain availability. Zero model tokens (Law 5/6). A hard failure means the engine
// must not start; a warning degrades gracefully (e.g. a missing optional toolchain -> that plugin's
// checks will defer, docs/18_PluginContract.md).
package doctor

import (
	"fmt"
	"os/exec"
	"sort"

	"claudworker/internal/config"
	"claudworker/internal/enginehome"
	"claudworker/internal/secrets"
)

// Status is a check outcome.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Check is a single preflight result.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Report is the full doctor result.
type Report struct {
	Checks []Check
}

// OK reports whether there were no Fail checks (warnings are allowed).
func (r *Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// Counts returns how many checks are ok/warn/fail.
func (r *Report) Counts() (ok, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Status {
		case OK:
			ok++
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}
	return
}

// toolLooker lets tests stub tool presence; production uses exec.LookPath.
type toolLooker func(name string) bool

func realLook(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// pluginToolchains maps a plugin name to the external tools its checks need. Missing tools are
// reported as warnings (the plugin's gates will defer), never hard failures at S0.
func pluginToolchains() map[string][]string {
	return map[string][]string{
		"flutter":           {"flutter"},
		"dotnet":            {"dotnet"},
		"web":               {"node", "npm"},
		"rest-api":          {"node"},
		"esp32-firmware":    {"pio"},
		"pcb-kicad":         {"kicad-cli"},
		"cad-3d":            {"freecadcmd"},
		"hardware-pipeline": {"python3"},
		"generic":           {},
	}
}

// Options configures a Run. Fields left nil use production defaults.
type Options struct {
	Resolver *secrets.Resolver
	LookPath toolLooker
}

// Run executes all checks for cfg and returns a structured report. It never returns an error for a
// failed check (that is reflected in the report); it only errors on an internal problem.
func Run(cfg *config.Config, opts Options) *Report {
	if opts.Resolver == nil {
		opts.Resolver = secrets.NewResolver()
	}
	if opts.LookPath == nil {
		opts.LookPath = realLook
	}
	r := &Report{}

	// 1. Config validity (cfg is already parsed+validated by config.Load; re-affirm defensively).
	if err := cfg.Validate(); err != nil {
		r.add("config", Fail, err.Error())
	} else {
		r.add("config", OK, fmt.Sprintf("project %q, %d repo(s)", cfg.Project, len(cfg.Repos)))
	}

	// 2. Engine home writable + layout.
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	if err := l.Writable(); err != nil {
		r.add("engine_home", Fail, err.Error())
	} else if missing := l.Missing(); len(missing) > 0 {
		r.add("engine_home", Warn, fmt.Sprintf("writable; %d dir(s) not yet created (run `cwv2 init`)", len(missing)))
	} else {
		r.add("engine_home", OK, l.Root)
	}

	// 3. Required base tools.
	for _, tool := range []string{"git", "claude"} {
		if opts.LookPath(tool) {
			r.add("tool:"+tool, OK, "found on PATH")
		} else {
			// git is required; claude is required for reasoning but not for S0 deterministic core.
			st := Fail
			hint := "required — install and ensure it is on PATH"
			if tool == "claude" {
				st = Warn
				hint = "not found — needed before any reasoning worker runs (S6+)"
			}
			r.add("tool:"+tool, st, hint)
		}
	}

	// 4. Secret resolvability (by name only; never reads a value).
	names := cfg.SecretNames()
	if len(names) == 0 {
		r.add("secrets", Warn, "no secrets referenced in config")
	} else {
		for _, n := range names {
			if opts.Resolver.CanResolve(n) {
				r.add("secret:"+n, OK, "resolvable")
			} else {
				r.add("secret:"+n, Warn, "not resolvable via keychain/Azure/env")
			}
		}
	}

	// 5. Per-plugin toolchains (missing => warning; that plugin's gates will defer).
	tc := pluginToolchains()
	for _, plugin := range distinctPlugins(cfg) {
		tools, known := tc[plugin]
		if !known {
			r.add("plugin:"+plugin, Warn, "unknown plugin — no toolchain probe registered")
			continue
		}
		var missing []string
		for _, t := range tools {
			if !opts.LookPath(t) {
				missing = append(missing, t)
			}
		}
		if len(missing) == 0 {
			r.add("plugin:"+plugin, OK, "toolchain present")
		} else {
			r.add("plugin:"+plugin, Warn, fmt.Sprintf("missing %v — its checks will defer", missing))
		}
	}

	return r
}

func (r *Report) add(name string, st Status, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: st, Detail: detail})
}

func distinctPlugins(cfg *config.Config) []string {
	seen := map[string]bool{}
	for _, repo := range cfg.Repos {
		if repo.Plugin != "" {
			seen[repo.Plugin] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
