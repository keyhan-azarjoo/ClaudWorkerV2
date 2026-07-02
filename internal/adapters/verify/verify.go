// Package verifyadapter is the REAL Verification edge (Phase B #2). It wraps the S8 verify.Engine +
// its verifier plugins (build/API/web/visual) and implements the orchestrator.Verifier port. No
// duplicated logic: the Engine owns capability selection, outcome aggregation, and evidence; this
// adapter just registers real plugins and runs the per-assignment verification plan.
//
// Visual/device verification uses the verify.VisualDriver seam (Appium/adb/WebDriver in production);
// headless verification (build/API/web) is used when visual interaction is impossible. Real device
// drivers require connected hardware — the contract + a fake driver are validated here; the live
// driver is wired when hardware is present.
package verifyadapter

import (
	"context"

	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// Adapter runs a verification plan (a set of verify.Requests) through a verify.Engine and returns the
// combined results. It satisfies orchestrator.Verifier: Verify(ctx, issue) → []verify.Result.
type Adapter struct {
	engine *verify.Engine
	plan   []verify.Request
}

// New builds an adapter from pre-registered verifiers and a verification plan. The plan is the set of
// checks run for every assignment (e.g. a build verifier + an API/web verifier).
func New(engine *verify.Engine, plan ...verify.Request) *Adapter {
	if engine == nil {
		engine = verify.New()
	}
	return &Adapter{engine: engine, plan: plan}
}

// Verify runs the plan for one assignment and returns every verifier's result (the Engine stamps
// verifier/type/duration; the improvement loop aggregates outcomes).
func (a *Adapter) Verify(ctx context.Context, issue string) ([]verify.Result, error) {
	if len(a.plan) == 0 {
		// Nothing configured to verify → Inconclusive (never a false pass/fail).
		return []verify.Result{{Verifier: "verifyadapter", Outcome: verify.Inconclusive, Summary: "no verification plan configured"}}, nil
	}
	var all []verify.Result
	for _, req := range a.plan {
		all = append(all, a.engine.Verify(ctx, req)...)
	}
	return all, nil
}

// BuildEngine assembles a verify.Engine with the standard real verifiers registered. Callers pass the
// concrete targets (repo dir for build, API/web URLs, a visual driver).
type Options struct {
	RepoDir    string              // for the build verifier (go build/test etc.)
	BuildCmd   []string            // build/test command (default: ["go","build","./..."])
	APIURL     string              // optional API health/contract URL
	WebURL     string              // optional website URL
	VisualName string              // optional visual verifier name
	VisualType verify.Type         // TypeVisual / TypeDevice / TypeUI
	VisualCaps []string            // capabilities the visual verifier provides
	VisualDrv  verify.VisualDriver // optional real driver (nil = no visual verifier)
}

// BuildEngine registers the real verifiers and returns the engine + the plan of requests to run.
func BuildEngine(o Options) (*verify.Engine, []verify.Request) {
	eng := verify.New()
	var plan []verify.Request

	cmd := o.BuildCmd
	if len(cmd) == 0 {
		cmd = []string{"go", "build", "./..."}
	}
	eng.Register(verify.CommandVerifier{VName: "build", VType: verify.TypeBuild, VCaps: []string{"build"}, Command: cmd, Dir: o.RepoDir})
	plan = append(plan, verify.Request{Type: verify.TypeBuild, Capabilities: []string{"build"}, Target: o.RepoDir})

	if o.APIURL != "" {
		eng.Register(verify.HTTPVerifier{VName: "api", VType: verify.TypeAPI, VCaps: []string{"http", "api"}, URL: o.APIURL, WantStatus: 200})
		plan = append(plan, verify.Request{Type: verify.TypeAPI, Capabilities: []string{"http"}, Target: o.APIURL})
	}
	if o.WebURL != "" {
		eng.Register(verify.HTTPVerifier{VName: "web", VType: verify.TypeUI, VCaps: []string{"http", "web"}, URL: o.WebURL, WantStatus: 200})
		plan = append(plan, verify.Request{Type: verify.TypeUI, Capabilities: []string{"http"}, Target: o.WebURL})
	}
	if o.VisualDrv != nil {
		t := o.VisualType
		if t == "" {
			t = verify.TypeVisual
		}
		eng.Register(verify.VisualVerifier{VName: orDefault(o.VisualName, "visual"), VType: t, VCaps: o.VisualCaps, Driver: o.VisualDrv})
	}
	return eng, plan
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
