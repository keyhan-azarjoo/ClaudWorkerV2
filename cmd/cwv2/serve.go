package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/adapters/discovery"
	gitadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/git"
	jiraadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/jira"
	runtimeadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/runtime"
	"github.com/myotgo/ClaudWorkerV2/internal/adapters/sim"
	verifyadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/verify"
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
	"github.com/myotgo/ClaudWorkerV2/internal/sentry"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// sentrySyncLoop periodically turns new Sentry errors into Jira bugs (enabled by CWV2_SENTRY_SYNC=1).
// It is idempotent + capped by sentrySync, so a sweep never floods the board.
func sentrySyncLoop(ctx context.Context, jc *jira.Client, scs []*sentry.Client, projectKey string, cp *controlplane.Server) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	run := func() {
		res, err := sentrySync(ctx, jc, scs, projectKey, 10)
		if err != nil {
			if cp != nil {
				cp.Bus().Publish("SentrySyncFailed", "sentry", map[string]any{"error": err.Error()})
			}
			return
		}
		if cp != nil {
			cp.Bus().Publish("SentrySynced", "sentry", res)
		}
	}
	run() // once at startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func serveUsage() {
	fmt.Fprint(os.Stderr, `cwv2 serve — run the Orchestrator + Control Plane (the serve loop)

  cwv2 serve --config <cwv2.yaml> [--mode live|simulation] [--bind :8080] [--web <ops-console dir>] [--once]

modes:
  simulation  run the FULL loop with deterministic adapters — no Claude/Jira/GitHub/devices/hardware
              (the regression + demo environment). Requires no credentials.
  live        real Jira + Git + Claude runtime + resource discovery + build/API/web verification.
              Device/visual verification drivers activate when hardware is connected.

  --once      run one orchestration step and exit (prints JSON). No HTTP server. Good for CI/demo.
`)
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	mode := fs.String("mode", "simulation", "live | simulation")
	bind := fs.String("bind", "127.0.0.1:8080", "HTTP bind address for the Control Plane API (loopback by default; set an explicit host + a dashboard.token to expose it)")
	web := fs.String("web", "", "directory of the Operations Console to serve at / (optional)")
	claudeBin := fs.String("claude-bin", "claude", "Claude Code CLI binary (live mode worker)")
	buildCmd := fs.String("build-cmd", "", "live-mode build verification command (default: go build ./...)")
	apiURL := fs.String("api-url", "", "live-mode API verification URL (optional)")
	webURL := fs.String("web-url", "", "live-mode website verification URL (optional)")
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

	o, cp, err := buildOrchestrator(*cfg, *mode, *claudeBin, verifyOpts{buildCmd: *buildCmd, apiURL: *apiURL, webURL: *webURL})
	if err != nil {
		return emitErr(err)
	}

	// Credentials HEALTH view (owner-facing): accounts + per-secret name/source/subsystem/status and
	// pass/fail validation. It NEVER returns a secret value (no reveal, no masked output). The
	// credentials.validate command confirms creds work (live Jira/GitHub checks) without exposing them.
	ch := newCredHealth(*cfg, o.Resources)
	cp.Query("credentials.health", func(_ context.Context, _ url.Values) (any, error) {
		return ch.snapshot(), nil
	})
	cp.Command("credentials.validate", func(ctx context.Context, _ []byte) (any, error) {
		return ch.validate(ctx), nil
	})

	// tasks.agents — live count of worker/subagent processes per task (for the dashboard box header).
	cp.Query("tasks.agents", func(_ context.Context, _ url.Values) (any, error) {
		return taskAgentCounts(), nil
	})

	// Account usage (5-hour + 7-day % used and reset times, like V1). Cached; refreshed on demand.
	um := newUsageMonitor(cfg.Accounts)
	cp.Query("accounts.usage", func(_ context.Context, _ url.Values) (any, error) {
		return um.snapshot(), nil
	})
	cp.Command("accounts.usage.refresh", func(ctx context.Context, _ []byte) (any, error) {
		return um.refresh(ctx, time.Now()), nil
	})
	// Keep the usage bars live: refresh the real subscription usage (headless OAuth endpoint) at startup
	// and every 5 minutes.
	go func() {
		um.refresh(context.Background(), time.Now())
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			um.refresh(context.Background(), time.Now())
		}
	}()

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
	// Multi-project on the SAME url: each sub-project is its OWN isolated cwv2 instance (own config,
	// secrets, engine home, tasks + data — nothing shared), reverse-proxied under /p/<slug>/. The
	// switcher reads projects.list. Sub-projects are added via scripts/cwv2-new-project.sh.
	regPath := projectsRegistryPath(*cfgPath)
	mux.Handle("/p/", &projectRouter{registryPath: regPath}) // dynamic — new projects proxy without restart
	cp.Query("projects.list", func(context.Context, url.Values) (any, error) {
		return projectsList(cfg.Project, loadProjectsRegistry(regPath)), nil
	})
	// Folder picker for the Add-Project UI (read-only directory listing).
	cp.Query("fs.dirs", func(_ context.Context, q url.Values) (any, error) {
		return listDirs(q.Get("path"))
	})
	// Create a NEW isolated project from the console (scaffold + register + start). Same URL, own data.
	cp.Command("projects.create", func(_ context.Context, body []byte) (any, error) {
		var in createProjectInput
		if err := json.Unmarshal(body, &in); err != nil {
			return nil, fmt.Errorf("bad request: %w", err)
		}
		return createProject(regPath, in)
	})
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

	// Device Log Monitor: capture ESP32 serial logs every ~6m, detect faults, auto-file Jira,
	// and expose them to the console (devicemonitor.* queries). Panic-guarded; exits on ctx.Done().
	StartDeviceMonitor(ctx, log, *cfg, cp)

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
// verifyOpts carries the live-mode verification targets (build/API/web) from serve flags.
type verifyOpts struct{ buildCmd, apiURL, webURL string }

func buildOrchestrator(cfg config.Config, mode, claudeBin string, vopts verifyOpts) (*orchestrator.Orchestrator, *controlplane.Server, error) {
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
	if live {
		// Real Resource Discovery (Phase B #1): probe accounts, local providers and devices via the
		// Resource Manager (no duplicated logic). Best-effort — missing tools/endpoints yield nothing.
		discoverLiveFleet(res, cfg.Accounts)
	}
	// Ensure at least one runtime account so the loop always runs (fallback / simulation default).
	if len(res.List(resource.Filter{Kind: resource.KindClaudeAccount})) == 0 {
		res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Name: "claude-1", Health: resource.HealthHealthy})
	}

	cp := controlplane.NewServer(controlplane.NewBus(), controlplane.WithAuth(controlplane.TokenAuth{Token: cfg.Dashboard.Token}))

	// Edges: REAL in live mode (Jira #1, Git #2), simulated otherwise.
	var (
		jiraPort   orchestrator.Jira      = sim.NewJira()
		devPort    orchestrator.Developer = &sim.Developer{}  // real Worker Runtime arrives in Phase 2.3
		mergePort  orchestrator.Merger    = sim.Merger{}      // becomes real via the Git adapter in live mode
		cleaner    orchestrator.Workspace                     // nil in simulation
		verifyPort orchestrator.Verifier  = sim.NewVerifier() // real in live
		liveJira   *jiraadapter.Adapter
		jiraClient *jira.Client
		gitA       *gitadapter.Adapter
		worker     *runtimeadapter.Worker
	)
	if live {
		email, token, err := jiraCreds(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("live mode: %w", err)
		}
		jiraClient = jira.New(cfg.Jira.BaseURL, email, token)
		liveJira = jiraadapter.New(jiraClient, cfg.Jira.WorkJQL)
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

		// Real Verification (Phase B #2): build (+ optional API/web) verifiers over the git clone.
		// Device/visual drivers are wired when hardware is present; build/API/web are headless.
		repoLocal := ""
		if len(cfg.Repos) > 0 {
			repoLocal = filepath.Join(l.ProjectDir, "repos", cfg.Repos[0].Name)
		}
		vo := verifyadapter.Options{RepoDir: repoLocal, APIURL: vopts.apiURL, WebURL: vopts.webURL}
		if vopts.buildCmd != "" {
			vo.BuildCmd = strings.Fields(vopts.buildCmd)
		}
		veng, vplan := verifyadapter.BuildEngine(vo)
		verifyPort = verifyadapter.New(veng, vplan...)
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
		Verifier:  verifyPort, // real in live (Phase B #2); sim otherwise
		Merger:    mergePort,
		Cleaner:   cleaner,
		Cfg:       orchestrator.Config{DevBranch: devBranch(cfg)},
	})
	o.RegisterControlPlane()

	// Persist per-task agent transcripts under the engine home so DONE tasks keep their full report
	// (survives restarts). Then stream each worker's live activity into that per-task log.
	if l.ProjectDir != "" {
		o.TaskLogDir = filepath.Join(l.ProjectDir, "task-logs")
	}
	if worker != nil {
		worker.OnLog = func(issue, line string) { o.AppendTaskLog(issue, line) }
		worker.OnTokens = func(issue string, in, out int) { o.SetTaskTokens(issue, in, out) }
		worker.OnTokensDone = func(issue string, in, out int) { o.BankTaskTokens(issue, in, out) }
	}

	// Standing RULES (Rules page): operator-defined rules injected into EVERY agent's prompt so the main
	// agent reads them before any change (e.g. cross-platform UI parity). Editable/toggle/delete.
	if l.ProjectDir != "" {
		rules := newRuleStore(l.ProjectDir)
		o.Rules = rules.activeTexts
		cp.Query("rules.list", func(context.Context, url.Values) (any, error) { return rules.load(), nil })
		cp.Command("rules.add", func(_ context.Context, body []byte) (any, error) {
			var r struct{ Title, Text string }
			_ = json.Unmarshal(body, &r)
			return rules.add(r.Title, r.Text)
		})
		cp.Command("rules.update", func(_ context.Context, body []byte) (any, error) {
			var r struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Text   string `json:"text"`
				Active bool   `json:"active"`
			}
			_ = json.Unmarshal(body, &r)
			return rules.update(r.ID, r.Title, r.Text, r.Active), nil
		})
		cp.Command("rules.setActive", func(_ context.Context, body []byte) (any, error) {
			var r struct {
				ID     string `json:"id"`
				Active bool   `json:"active"`
			}
			_ = json.Unmarshal(body, &r)
			return rules.setActive(r.ID, r.Active), nil
		})
		cp.Command("rules.remove", func(_ context.Context, body []byte) (any, error) {
			var r struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(body, &r)
			return rules.remove(r.ID), nil
		})
	}

	// Repositories (Git page): the project's managed repos with active/inactive state + org discovery.
	// Deactivating EVERY repo turns the project OFF — the work gate then refuses all agent work.
	if live && l.ProjectDir != "" {
		rs := newRepoStore(l.ProjectDir, cfg.Repos)
		o.WorkAllowed = func() (bool, string) {
			if rs.anyActive() {
				return true, ""
			}
			return false, "all repositories are deactivated"
		}
		cp.Query("repos.list", func(context.Context, url.Values) (any, error) { return rs.load(), nil })
		cp.Query("github.repos", func(ctx context.Context, q url.Values) (any, error) {
			return githubRepos(ctx, q.Get("owner"))
		})
		cp.Command("repos.add", func(_ context.Context, body []byte) (any, error) {
			var r struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			}
			_ = json.Unmarshal(body, &r)
			return rs.add(r.Name, r.URL)
		})
		cp.Command("repos.remove", func(_ context.Context, body []byte) (any, error) {
			var r struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(body, &r)
			return rs.remove(r.Name), nil
		})
		cp.Command("repos.setActive", func(_ context.Context, body []byte) (any, error) {
			var r struct {
				Name   string `json:"name"`
				Active bool   `json:"active"`
			}
			_ = json.Unmarshal(body, &r)
			return rs.setActive(r.Name, r.Active), nil
		})
	}

	// Live Jira page becomes real.
	if liveJira != nil {
		projectKey := projectKeyFromJQL(cfg.Jira.WorkJQL)
		cp.Query("jira.queue", func(ctx context.Context, _ url.Values) (any, error) { return liveJira.Queue(ctx) })
		// jira.backlog: ALL project tasks, highest priority first (the console shows the real board).
		cp.Query("jira.backlog", func(ctx context.Context, _ url.Values) (any, error) {
			return jiraBacklog(ctx, jiraClient, projectKey)
		})
		// Sentry → Jira: create a HIGH-priority Bug for each new Sentry error (labelled, deduped, capped;
		// no "ready" label so no agent auto-runs it — the operator Runs it when they want).
		scs := sentryClients() // one client per configured Sentry org (both myotgo orgs)
		// Read-only preview of recent Sentry errors (no tickets created) — verifies connectivity + lets
		// the operator see what a sync would turn into bugs.
		cp.Query("sentry.errors", func(ctx context.Context, _ url.Values) (any, error) {
			if len(scs) == 0 {
				return []any{}, nil
			}
			return recentFromAll(ctx, scs, "14d", 25)
		})
		cp.Command("sentry.sync", func(ctx context.Context, _ []byte) (any, error) {
			return sentrySync(ctx, jiraClient, scs, projectKey, 10)
		})
		// Optional periodic sync (off by default; set CWV2_SENTRY_SYNC=1 to enable a background sweep).
		if os.Getenv("CWV2_SENTRY_SYNC") == "1" && len(scs) > 0 {
			go sentrySyncLoop(context.Background(), jiraClient, scs, projectKey, cp)
		}
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

// discoverLiveFleet runs real discovery (best-effort) into the Resource Manager: local model
// providers, Android/iOS-sim/ESP32 devices, and Claude accounts. When the config lists accounts they
// are used verbatim (only those known accounts); otherwise it falls back to folder auto-discovery.
func discoverLiveFleet(res *resource.Manager, accts []config.Account) {
	comp := discovery.Composite{
		discovery.Provider{ID: "ollama", BaseURL: "http://127.0.0.1:11434", Ollama: true},
		discovery.Provider{ID: "lmstudio", BaseURL: "http://127.0.0.1:1234"},
		discovery.Adb{},
		discovery.Simctl{},
		discovery.Serial{},
	}
	if len(accts) == 0 {
		// No configured accounts → fall back to auto-discovering ~/.cw-accounts (Claude only).
		comp = append(comp, discovery.Accounts{Kind: resource.KindClaudeAccount, Dirs: defaultClaudeDirs()})
	}
	_ = res.Discover(comp) // best-effort; never fatal
	// Configured accounts are registered directly so each carries its own ENGINE (claude|codex),
	// making both selectable + routable to the right CLI.
	registerConfiguredAccounts(res, accts)
}

// registerConfiguredAccounts registers each configured account (claude + codex) as a worker-account
// resource with an engine label, so only known/working accounts appear and each routes to its CLI.
func registerConfiguredAccounts(res *resource.Manager, accts []config.Account) {
	home, _ := os.UserHomeDir()
	for _, a := range accts {
		engine := a.Engine
		if engine == "" {
			engine = "claude"
		}
		dir := a.ConfigDir
		if strings.HasPrefix(dir, "~/") && home != "" {
			dir = filepath.Join(home, dir[2:])
		}
		name := a.Name
		if name == "" {
			name = filepath.Base(dir)
		}
		health := resource.HealthHealthy
		if dir != "" {
			if _, err := os.Stat(dir); err != nil {
				health = resource.HealthDown
			}
		}
		res.Register(resource.Resource{
			ID: "acct-" + accSlug(name), Kind: resource.KindClaudeAccount, Name: name, Health: health,
			Labels: map[string]string{"engine": engine, "claude_config_dir": dir, "model": a.Model, "source": "config"},
		})
	}
}

// accSlug lowercases a name to a stable resource-id suffix (letters/digits kept, others → '-').
func accSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// defaultClaudeDirs finds Claude account config dirs under the conventional locations.
func defaultClaudeDirs() map[string]string {
	out := map[string]string{}
	home, _ := os.UserHomeDir()
	if home == "" {
		return out
	}
	for _, base := range []string{filepath.Join(home, ".cw-accounts")} {
		if entries, err := os.ReadDir(base); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					out[e.Name()] = filepath.Join(base, e.Name())
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		out["default"] = filepath.Join(home, ".claude")
	}
	return out
}

// accountsFrom maps the Resource Manager's Claude-account resources to executable runtime accounts.
// The mapping only carries execution config (config dir label); the Resource Manager still SELECTS.
func accountsFrom(res *resource.Manager) map[string]runtimeadapter.Account {
	out := map[string]runtimeadapter.Account{}
	for _, r := range res.List(resource.Filter{Kind: resource.KindClaudeAccount}) {
		out[r.ID] = runtimeadapter.Account{ID: r.ID, ConfigDir: r.Labels["claude_config_dir"], Model: r.Labels["model"], Engine: r.Labels["engine"]}
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
