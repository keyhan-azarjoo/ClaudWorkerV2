package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	gitadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/git"
	jiraadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/jira"
	runtimeadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/runtime"
	"github.com/myotgo/ClaudWorkerV2/internal/adapters/sim"
	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/config"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	"github.com/myotgo/ClaudWorkerV2/internal/enginehome"
	git "github.com/myotgo/ClaudWorkerV2/internal/git"
	jira "github.com/myotgo/ClaudWorkerV2/internal/jira"
	"github.com/myotgo/ClaudWorkerV2/internal/knowledge"
	"github.com/myotgo/ClaudWorkerV2/internal/lease"
	"github.com/myotgo/ClaudWorkerV2/internal/logging"
	"github.com/myotgo/ClaudWorkerV2/internal/orchestrator"
	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/resource"
	"github.com/myotgo/ClaudWorkerV2/internal/secrets"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

func serveUsage() {
	fmt.Fprint(os.Stderr, `cwv2 serve — run the Orchestrator + Control Plane (the serve loop)

  cwv2 serve --config <cwv2.yaml> [--mode live|simulation] [--bind :8080] [--web <ops-console dir>] [--once]

modes:
  simulation  run the FULL loop with deterministic adapters — no Claude/Jira/GitHub/devices/hardware
              (the regression + demo environment). Requires no credentials.
  live        use the real Jira adapter (Phase 2 #1). Worker/Verify/Merge/Resource-discovery are still
              simulated until their integration iterations, so the platform stays fully functional.

  --once      run one orchestration step and exit (prints JSON). No HTTP server. Good for CI/demo.
`)
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	mode := fs.String("mode", "simulation", "live | simulation")
	bind := fs.String("bind", ":8080", "HTTP bind address for the Control Plane API")
	web := fs.String("web", "", "directory of the Operations Console to serve at / (optional)")
	claudeBin := fs.String("claude-bin", "claude", "Claude Code CLI binary (live mode worker)")
	once := fs.Bool("once", false, "run one orchestration step and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 serve: --config is required")
		serveUsage()
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(err)
	}
	log := logging.Default()

	o, cp, err := buildOrchestrator(*cfg, *mode, *claudeBin)
	if err != nil {
		return emitErr(err)
	}

	if *once {
		did, err := o.ProcessOnce(context.Background())
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"mode": *mode, "processed": did})
		return 0
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// HTTP: Control Plane API under /v1, optional static Operations Console at /.
	mux := http.NewServeMux()
	mux.Handle("/v1/", cp.Handler())
	if *web != "" {
		mux.Handle("/", http.FileServer(http.Dir(*web)))
	}
	srv := &http.Server{Addr: *bind, Handler: mux}
	go func() {
		log.Info("control plane listening", "addr", *bind, "mode", *mode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server", "error", err.Error())
			stop()
		}
	}()

	// Run the orchestration loop until shutdown.
	errCh := make(chan error, 1)
	go func() { errCh <- o.Run(ctx) }()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	<-errCh
	return 0
}

// buildOrchestrator wires all subsystems for the given mode. Live durably persists state (file
// stores); simulation uses in-memory stores. Only the Jira edge is real in live mode today (Phase 2
// #1); the remaining edges use the deterministic sim adapters until their iterations, keeping the
// platform fully functional at every step.
func buildOrchestrator(cfg config.Config, mode, claudeBin string) (*orchestrator.Orchestrator, *controlplane.Server, error) {
	live := mode == "live"
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	if err := l.Ensure(); err != nil {
		return nil, nil, err
	}

	// Stores: durable (live) vs in-memory (simulation).
	var (
		store  assignment.Store
		leaseS lease.Store
		knowS  knowledge.Store
		err    error
	)
	if live {
		if store, err = assignment.NewFileStore(l.Assignments); err != nil {
			return nil, nil, err
		}
		if leaseS, err = lease.NewFileStore(filepath.Join(l.ProjectDir, "leases")); err != nil {
			return nil, nil, err
		}
		if knowS, err = knowledge.NewFileStore(l.KnowledgeEntries); err != nil {
			return nil, nil, err
		}
	} else {
		store, leaseS, knowS = assignment.NewMemoryStore(), lease.NewMemoryStore(), knowledge.NewMemoryStore()
	}

	res := resource.New()
	// Until real resource discovery (Phase 2 #6), register a default runtime account so the loop runs.
	res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Name: "claude-1", Health: resource.HealthHealthy})

	cp := controlplane.NewServer(controlplane.NewBus(), controlplane.WithAuth(controlplane.TokenAuth{Token: cfg.Dashboard.Token}))

	// Edges: REAL in live mode (Jira #1, Git #2), simulated otherwise.
	var (
		jiraPort  orchestrator.Jira      = sim.NewJira()
		devPort   orchestrator.Developer = &sim.Developer{} // real Worker Runtime arrives in Phase 2.3
		mergePort orchestrator.Merger    = sim.Merger{}     // becomes real via the Git adapter in live mode
		cleaner   orchestrator.Workspace                    // nil in simulation
		liveJira  *jiraadapter.Adapter
		gitA      *gitadapter.Adapter
		worker    *runtimeadapter.Worker
	)
	if live {
		email, token, err := jiraCreds(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("live mode: %w", err)
		}
		liveJira = jiraadapter.New(jira.New(cfg.Jira.BaseURL, email, token), cfg.Jira.WorkJQL)
		jiraPort = liveJira

		gitA, err = buildGit(cfg, l)
		if err != nil {
			return nil, nil, fmt.Errorf("live mode git: %w", err)
		}
		// Real Worker Runtime (Claude Code), executing under the Resource-Manager-selected account.
		worker = runtimeadapter.New(claudeBin, accountsFrom(res))
		worker.Cooldown = func(account string, d time.Duration) { res.Cooldown(account, time.Now().Add(d)) }
		worker.OnMetrics = func(m runtimeadapter.Metrics) { cp.Bus().Publish("RuntimeMetrics", "runtime", m) }

		devPort = gitadapter.NewDeveloper(gitA, worker) // real Git workspace + REAL Claude worker
		mergePort = gitadapter.NewMerger(gitA)          // real --no-ff merge
		cleaner = gitA                                  // real worktree/branch cleanup on terminal
	}

	o := orchestrator.New(&orchestrator.Orchestrator{
		Resources: res,
		Policy:    policy.New(policy.FromConfig(cfg)),
		Leases:    lease.New(leaseS),
		Knowledge: knowledge.New(knowS),
		Verify:    verify.New(),
		Store:     store,
		CP:        cp,
		Jira:      jiraPort,
		Developer: devPort,
		Verifier:  sim.NewVerifier(), // real Verification arrives in Phase 2.4
		Merger:    mergePort,
		Cleaner:   cleaner,
		Cfg:       orchestrator.Config{DevBranch: devBranch(cfg)},
	})
	o.RegisterControlPlane()

	// Live Jira page becomes real.
	if liveJira != nil {
		cp.Query("jira.queue", func(ctx context.Context, _ url.Values) (any, error) { return liveJira.Queue(ctx) })
	}
	// Live Git state → Control Plane (active worktrees, merge/conflict/cleanup status).
	if gitA != nil {
		cp.Query("git.worktrees", func(ctx context.Context, _ url.Values) (any, error) { return gitA.Worktrees(ctx) })
		cp.Query("git.status", func(ctx context.Context, _ url.Values) (any, error) { return gitA.Status(ctx) })
	}
	// Live Worker Runtime state → Control Plane (active executions, accounts, cooldowns, failover).
	if worker != nil {
		cp.Query("runtime.state", func(context.Context, url.Values) (any, error) { return worker.Snapshot(), nil })
	}
	return o, cp, nil
}

// accountsFrom maps the Resource Manager's Claude-account resources to executable runtime accounts.
// The mapping only carries execution config (config dir label); the Resource Manager still SELECTS.
func accountsFrom(res *resource.Manager) map[string]runtimeadapter.Account {
	out := map[string]runtimeadapter.Account{}
	for _, r := range res.List(resource.Filter{Kind: resource.KindClaudeAccount}) {
		out[r.ID] = runtimeadapter.Account{ID: r.ID, ConfigDir: r.Labels["claude_config_dir"], Model: r.Labels["model"]}
	}
	return out
}

// buildGit prepares the engine's dedicated clone and returns the real Git adapter. The clone lives
// under the engine home; per-assignment work happens in disposable worktrees (the main tree is never
// touched). Live mode requires a reachable repo.
func buildGit(cfg config.Config, l enginehome.Layout) (*gitadapter.Adapter, error) {
	if len(cfg.Repos) == 0 {
		return nil, fmt.Errorf("no repo configured")
	}
	repo := cfg.Repos[0]
	g := git.New(git.WithIdentity(git.Identity{Name: cfg.GitHub.CommitIdentity.Name, Email: cfg.GitHub.CommitIdentity.Email}))
	local := filepath.Join(l.ProjectDir, "repos", repo.Name)
	if _, err := os.Stat(filepath.Join(local, ".git")); os.IsNotExist(err) {
		if err := g.Clone(context.Background(), repo.URL, local); err != nil {
			return nil, fmt.Errorf("clone %s: %w", repo.URL, err)
		}
	}
	dev := repo.DevBranch
	if dev == "" {
		dev = "development"
	}
	return gitadapter.New(g, local, dev, filepath.Join(l.Worktrees, repo.Name)), nil
}

// jiraCreds resolves the Jira email + API token from the configured secret names.
func jiraCreds(cfg config.Config) (email, token string, err error) {
	if cfg.Jira.Auth.TokenSecret == "" {
		return "", "", fmt.Errorf("jira.auth.token_secret is required for live mode")
	}
	r := secrets.NewResolver()
	if token, err = r.Resolve(cfg.Jira.Auth.TokenSecret); err != nil {
		return "", "", err
	}
	email = cfg.Jira.Auth.UserSecret
	if email != "" {
		if v, e := r.Resolve(email); e == nil {
			email = v
		}
	}
	return email, token, nil
}

func devBranch(cfg config.Config) string {
	for _, r := range cfg.Repos {
		if r.DevBranch != "" {
			return r.DevBranch
		}
	}
	return "development"
}
