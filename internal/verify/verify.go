// Package verify is the Verification Engine (docs/06, docs/21 S8 — renamed from QA).
//
// It owns ONLY verification. It does not decide, repair, merge, update Jira, or manage assignments —
// it verifies work and reports what it found. "QA / human-like UI testing" is just one verification
// capability among many (visual, ui, api, unit, integration, build, device, hardware, pcb, 3d,
// documentation).
//
// Plugin model (capability-based, future-proof). Verifiers are plugins registered with the Engine.
// The Assignment Engine REQUESTS verification (a Type + required Capabilities); the Engine chooses the
// matching plugins and runs them. New verification kinds are added by registering a new Verifier —
// the core never changes.
package verify

import (
	"context"
	"sort"
	"time"
)

// Type is a verification category. It is an open vocabulary (a string) so future plugins add new
// types without touching the core; these are the documented ones.
type Type string

const (
	TypeVisual        Type = "visual"        // human-like end-to-end interaction
	TypeUI            Type = "ui"            // UI structure/state checks
	TypeAPI           Type = "api"           // API contract checks
	TypeUnit          Type = "unit"          // unit tests
	TypeIntegration   Type = "integration"   // integration tests
	TypeBuild         Type = "build"         // build/compile
	TypeDevice        Type = "device"        // on-device behaviour
	TypeHardware      Type = "hardware"      // hardware behaviour
	TypePCB           Type = "pcb"           // PCB checks (DRC/ERC/etc.)
	Type3D            Type = "3d"            // 3D model checks (fit/print)
	TypeDocumentation Type = "documentation" // docs checks
)

// Outcome is the verdict of a verification. Exactly these five (owner-mandated).
type Outcome string

const (
	Pass         Outcome = "pass"
	Fail         Outcome = "fail"
	Blocked      Outcome = "blocked"      // cannot verify (missing capability / environment down)
	Deferred     Outcome = "deferred"     // postponed (e.g. resource unavailable now)
	Inconclusive Outcome = "inconclusive" // ran but could not determine
)

// severity orders outcomes for aggregation (higher wins). Fail is worst; Pass is best.
func severity(o Outcome) int {
	switch o {
	case Fail:
		return 4
	case Blocked:
		return 3
	case Inconclusive:
		return 2
	case Deferred:
		return 1
	default: // Pass
		return 0
	}
}

// Evidence is a produced artifact (screenshot, diff, OCR text, log file, report path).
type Evidence struct {
	Kind string `json:"kind"` // "screenshot" | "diff" | "ocr" | "artifact" | ...
	Ref  string `json:"ref"`  // path/url/inline reference
	Note string `json:"note,omitempty"`
}

// Result is what EVERY verifier returns: an outcome plus evidence, metrics, duration, and logs.
type Result struct {
	Verifier string             `json:"verifier"` // plugin name (set by the Engine)
	Type     Type               `json:"type"`     // verification type (set by the Engine)
	Outcome  Outcome            `json:"outcome"`
	Summary  string             `json:"summary"`
	Detail   string             `json:"detail,omitempty"` // differences / failure detail
	Evidence []Evidence         `json:"evidence,omitempty"`
	Metrics  map[string]float64 `json:"metrics,omitempty"`
	Duration time.Duration      `json:"duration"` // set by the Engine (wall-clock)
	Logs     []string           `json:"logs,omitempty"`
}

// Step is one human-like interaction (used by interactive verifiers; ignored by others).
type Step struct {
	Action   string // "navigate" | "click" | "type" | "scroll" | "pair"
	Selector string
	Value    string
}

// Expectation is a check to evaluate after the steps run.
type Expectation struct {
	Description string
	Kind        string // "text_present" | "state"
	Key         string // for "state": the state key ("text_present" uses Value)
	Value       string
}

// Request is what the Assignment Engine sends. It describes WHAT to verify and the required
// capabilities — never anything about decisions, repair, merge, or Jira (verification only).
type Request struct {
	Type         Type              // requested verification type
	Capabilities []string          // capabilities the chosen verifier must provide
	Target       string            // what to verify (repo path, app id, device id, url, model file…)
	Steps        []Step            // optional interaction steps (visual/ui/device)
	Expectations []Expectation     // what to assert
	Params       map[string]string // plugin-specific parameters
}

// Verifier is the plugin contract. A verifier declares the Type it covers and the Capabilities it
// provides; the Engine selects it when a Request matches. Verifiers ONLY verify.
type Verifier interface {
	Name() string
	Type() Type
	Capabilities() []string
	Verify(ctx context.Context, req Request) (Result, error)
}

// Engine registers verifier plugins and dispatches requests to the matching ones. It adds no
// verification logic of its own — it selects, times, and labels results.
type Engine struct {
	verifiers []Verifier
	now       func() time.Time
}

// Option configures the Engine.
type Option func(*Engine)

// WithClock overrides the time source (tests inject a controllable clock for deterministic duration).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// New builds an Engine with the given verifiers registered.
func New(verifiers ...Verifier) *Engine {
	return &Engine{verifiers: append([]Verifier(nil), verifiers...), now: time.Now}
}

// WithOptions is New plus options.
func WithOptions(opts []Option, verifiers ...Verifier) *Engine {
	e := New(verifiers...)
	for _, o := range opts {
		o(e)
	}
	return e
}

// Register adds a verifier plugin at runtime (future-proofing: no core change to add a kind).
func (e *Engine) Register(v Verifier) { e.verifiers = append(e.verifiers, v) }

// Select returns every registered verifier that matches the request: same Type and providing all the
// requested Capabilities. Deterministic order (by name).
func (e *Engine) Select(req Request) []Verifier {
	var out []Verifier
	for _, v := range e.verifiers {
		if v.Type() != req.Type {
			continue
		}
		if provides(v.Capabilities(), req.Capabilities) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Verify chooses the appropriate plugin(s) and runs them, returning one Result per verifier. When no
// verifier matches, it returns a single Blocked result (verification cannot proceed) — it never
// silently returns nothing. The Engine sets Verifier/Type/Duration; the plugin owns the verdict.
func (e *Engine) Verify(ctx context.Context, req Request) []Result {
	selected := e.Select(req)
	if len(selected) == 0 {
		return []Result{{
			Verifier: "engine", Type: req.Type, Outcome: Blocked,
			Summary: "no capable verifier registered for this type/capabilities",
		}}
	}
	results := make([]Result, 0, len(selected))
	for _, v := range selected {
		start := e.now()
		res, err := v.Verify(ctx, req)
		res.Verifier = v.Name()
		res.Type = v.Type()
		res.Duration = e.now().Sub(start)
		if err != nil {
			// A plugin ERROR means verification could not complete → Inconclusive (not a Fail: we did
			// not observe the work failing, only that we could not verify it).
			if res.Outcome == "" {
				res.Outcome = Inconclusive
			}
			if res.Summary == "" {
				res.Summary = "verifier error: " + err.Error()
			}
			res.Logs = append(res.Logs, "error: "+err.Error())
		}
		results = append(results, res)
	}
	return results
}

// Aggregate reduces many results to a single overall outcome: the most severe wins
// (Fail > Blocked > Inconclusive > Deferred > Pass). Empty input is Inconclusive.
func Aggregate(results []Result) Outcome {
	if len(results) == 0 {
		return Inconclusive
	}
	worst := Pass
	for _, r := range results {
		if severity(r.Outcome) > severity(worst) {
			worst = r.Outcome
		}
	}
	return worst
}

func provides(have, need []string) bool {
	if len(need) == 0 {
		return true
	}
	set := make(map[string]bool, len(have))
	for _, c := range have {
		set[c] = true
	}
	for _, n := range need {
		if !set[n] {
			return false
		}
	}
	return true
}
