// Command cwv2 is the ClaudWorker V2 engine binary.
//
// Subsystem S0 (docs/21_ImplementationRoadmap.md) ships the deterministic foundations only:
//
//	cwv2 version              print the binary + spec version
//	cwv2 init    --config P   create the engine-home layout for the config's project
//	cwv2 doctor  --config P   validate config + environment (zero tokens)
//
// Later subsystems add `serve`, `tool`, `migrate`, etc. This binary spends NO model tokens.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/myotgo/ClaudWorkerV2/internal/config"
	"github.com/myotgo/ClaudWorkerV2/internal/doctor"
	"github.com/myotgo/ClaudWorkerV2/internal/enginehome"
	"github.com/myotgo/ClaudWorkerV2/internal/logging"
)

// SpecVersion is the architecture spec this binary targets (SPEC_VERSION.md).
const SpecVersion = "2.1.0"

// Version is the binary build version (overridable at build time via -ldflags).
var Version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "version", "-v", "--version":
		fmt.Printf("cwv2 %s (spec v%s)\n", Version, SpecVersion)
		return 0
	case "doctor":
		return cmdDoctor(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "git":
		return cmdGit(args[1:])
	case "jira":
		return cmdJira(args[1:])
	case "assignment":
		return cmdAssignment(args[1:])
	case "knowledge":
		return cmdKnowledge(args[1:])
	case "worker":
		return cmdWorker(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "cwv2: unknown command %q\n\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cwv2 — ClaudWorker V2 engine (S0 foundations)

usage:
  cwv2 version
  cwv2 init   --config <cwv2.yaml>
  cwv2 doctor --config <cwv2.yaml> [--json]
  cwv2 git    <subcommand> --repo <path> ...      (deterministic Git toolbelt; JSON output)
  cwv2 jira   <subcommand> --config <cwv2.yaml> ... (deterministic Jira toolbelt; JSON output)
  cwv2 assignment list --config <cwv2.yaml>       (inspect the Assignment store)
  cwv2 knowledge  <subcommand> --config <cwv2.yaml> (Knowledge Brain; deterministic, zero tokens)
  cwv2 worker     prompt ...                       (render a Worker Runtime prompt; zero tokens)
  cwv2 serve      --config <cwv2.yaml> [--mode live|simulation]  (run the Orchestrator + Control Plane)

  cwv2 git        help
  cwv2 jira       help
  cwv2 assignment help
  cwv2 knowledge  help
  cwv2 worker     help
`)
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	asJSON := fs.Bool("json", false, "emit JSON logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 doctor: --config is required")
		return 2
	}
	format := "text"
	if *asJSON {
		format = "json"
	}
	log := logging.New(os.Stderr, "info", format)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config invalid", "error", err.Error())
		return 1
	}
	rep := doctor.Run(cfg, doctor.Options{})
	for _, c := range rep.Checks {
		switch c.Status {
		case doctor.Fail:
			log.Error("check", "name", c.Name, "status", c.Status, "detail", c.Detail)
		case doctor.Warn:
			log.Warn("check", "name", c.Name, "status", c.Status, "detail", c.Detail)
		default:
			log.Info("check", "name", c.Name, "status", c.Status, "detail", c.Detail)
		}
	}
	ok, warn, fail := rep.Counts()
	if rep.OK() {
		log.Info("doctor: PASS", "ok", ok, "warn", warn, "fail", fail)
		return 0
	}
	log.Error("doctor: FAIL", "ok", ok, "warn", warn, "fail", fail)
	return 1
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 init: --config is required")
		return 2
	}
	log := logging.Default()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config invalid", "error", err.Error())
		return 1
	}
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	if err := l.Ensure(); err != nil {
		log.Error("init failed", "error", err.Error())
		return 1
	}
	log.Info("engine home ready", "project", cfg.Project, "root", l.Root)
	return 0
}
