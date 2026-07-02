// Package stress runs large-scale, deterministic validation of the orchestration loop in Simulation
// Mode — no Claude/Jira/GitHub/hardware. It drives many issues through real subsystems (resource,
// policy, lease, knowledge, control plane, durable stores) with injected failures (verification
// failures → improvement loops, merge conflicts → failures) and a mid-run CRASH + RESTART, then
// proves every issue reaches a terminal state exactly once and results are byte-for-byte repeatable.
package stress

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/adapters/sim"
	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	"github.com/myotgo/ClaudWorkerV2/internal/knowledge"
	"github.com/myotgo/ClaudWorkerV2/internal/lease"
	"github.com/myotgo/ClaudWorkerV2/internal/orchestrator"
	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/resource"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// Config parameterises a stress run.
type Config struct {
	Issues        int    // number of Jira issues (e.g. 100+)
	RestartAfter  int    // simulate a crash + restart after this many claimed issues (0 = none)
	Dir           string // durable store dir (temp); "" = caller supplies via Run's arg
	VerifyFailMod int    // issue index % this → verification fails once first (improvement loop). 0 = 4
	ConflictMod   int    // issue index % this → merge conflict → Failed. 0 = 7
}

// Report is the outcome of a stress run.
type Report struct {
	Issues    int           `json:"issues"`
	Done      int           `json:"done"`
	Failed    int           `json:"failed"`
	Deferred  int           `json:"deferred"`
	Terminal  int           `json:"terminal"`
	Rounds    int           `json:"rounds"`
	Restarted bool          `json:"restarted"`
	Elapsed   time.Duration `json:"elapsed_ns"`
	Signature string        `json:"signature"` // deterministic fingerprint of the outcome
}

// stressVerifier fails the first attempt for issues matching VerifyFailMod, then passes — exercising
// the improvement loop deterministically (no RNG).
type stressVerifier struct {
	failMod int
	seen    map[string]int
}

func (v *stressVerifier) Verify(_ context.Context, issue string) ([]verify.Result, error) {
	n := indexOf(issue)
	v.seen[issue]++
	if v.failMod > 0 && n%v.failMod == 0 && v.seen[issue] == 1 {
		return []verify.Result{{Verifier: "stress", Outcome: verify.Fail, Detail: "injected"}}, nil
	}
	return []verify.Result{{Verifier: "stress", Outcome: verify.Pass}}, nil
}

// stressMerger conflicts (returns not-merged) for issues matching ConflictMod → the orchestrator fails
// them; others merge.
type stressMerger struct{ conflictMod int }

func (m stressMerger) Merge(_ context.Context, issue string) (bool, error) {
	if m.conflictMod > 0 && indexOf(issue)%m.conflictMod == 0 {
		return false, nil // conflict (deterministic)
	}
	return true, nil
}

func issues(n int) []orchestrator.Issue {
	out := make([]orchestrator.Issue, n)
	for i := 0; i < n; i++ {
		out[i] = orchestrator.Issue{Key: fmt.Sprintf("STRESS-%d", i+1), Summary: "task", AcceptanceCriteria: "- done"}
	}
	return out
}

func indexOf(issue string) int {
	num, mult := 0, 1
	for i := len(issue) - 1; i >= 0; i-- {
		if issue[i] < '0' || issue[i] > '9' {
			break
		}
		num += int(issue[i]-'0') * mult
		mult *= 10
	}
	return num
}

func build(dir string, cfg Config) *orchestrator.Orchestrator {
	astore, _ := assignment.NewFileStore(filepath.Join(dir, "assignments"))
	lstore, _ := lease.NewFileStore(filepath.Join(dir, "leases"))
	res := resource.New()
	res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	return orchestrator.New(&orchestrator.Orchestrator{
		Resources: res,
		Policy:    policy.New(policy.Config{}),
		Leases:    lease.New(lstore),
		Knowledge: knowledge.New(knowledge.NewMemoryStore()),
		Verify:    verify.New(),
		Store:     astore,
		CP:        controlplane.NewServer(controlplane.NewBus()),
		Jira:      sim.NewJira(issues(cfg.Issues)...),
		Developer: &sim.Developer{},
		Verifier:  &stressVerifier{failMod: orInt(cfg.VerifyFailMod, 4), seen: map[string]int{}},
		Merger:    stressMerger{conflictMod: orInt(cfg.ConflictMod, 7)},
		Cfg:       orchestrator.Config{DevBranch: "development"},
	})
}

// Run executes the stress scenario in dir (durable stores) and returns a report.
func Run(dir string, cfg Config) (Report, error) {
	start := time.Now()
	rep := Report{Issues: cfg.Issues}
	ctx := context.Background()

	o := build(dir, cfg)

	// Phase 1: claim + process up to RestartAfter, then simulate a crash by dropping the orchestrator.
	claimed := 0
	for {
		did, err := o.ProcessOnce(ctx)
		if err != nil {
			return rep, err
		}
		if !did {
			break
		}
		claimed++
		if cfg.RestartAfter > 0 && claimed >= cfg.RestartAfter {
			rep.Restarted = true
			break // "crash"
		}
	}

	// Phase 2: RESTART — a brand-new orchestrator over the SAME durable stores; recover, then drain.
	rounds := 0
	for {
		rounds++
		o = build(dir, cfg) // fresh process
		if err := o.Recover(ctx); err != nil {
			return rep, err
		}
		did := true
		for did {
			var err error
			did, err = o.ProcessOnce(ctx)
			if err != nil {
				return rep, err
			}
		}
		// Are all issues terminal now?
		done, failed, deferred := tally(o)
		if done+failed == cfg.Issues || rounds > cfg.Issues+5 {
			rep.Done, rep.Failed, rep.Deferred = done, failed, deferred
			break
		}
	}

	rep.Rounds = rounds
	rep.Terminal = rep.Done + rep.Failed
	rep.Elapsed = time.Since(start)
	rep.Signature = fmt.Sprintf("issues=%d done=%d failed=%d", rep.Issues, rep.Done, rep.Failed)
	return rep, nil
}

func tally(o *orchestrator.Orchestrator) (done, failed, deferred int) {
	all, _ := o.Store.List()
	for _, a := range all {
		switch a.State {
		case assignment.StateDone:
			done++
		case assignment.StateFailed:
			failed++
		default:
			deferred++
		}
	}
	return
}

func orInt(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}
