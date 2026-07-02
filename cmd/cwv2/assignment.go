package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/config"
	"github.com/myotgo/ClaudWorkerV2/internal/enginehome"
)

func assignmentUsage() {
	fmt.Fprint(os.Stderr, `cwv2 assignment — inspect the Assignment store (JSON output)

subcommands (require --config <cwv2.yaml>):
  list        list all persisted Assignments (id, issue, state, attempt)
`)
}

// cmdAssignment exposes the S2 Assignment store. Driving/claiming runs inside the daemon (later
// subsystems) and needs live Jira; S2 ships the read-only `list` for inspection.
func cmdAssignment(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		assignmentUsage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	sub := args[0]
	fs := flag.NewFlagSet("assignment "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 assignment: --config is required")
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(err)
	}
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	store, err := assignment.NewFileStore(l.Assignments)
	if err != nil {
		return emitErr(err)
	}

	switch sub {
	case "list":
		all, err := store.List()
		if err != nil {
			return emitErr(err)
		}
		type row struct {
			Issue   string `json:"issue"`
			State   string `json:"state"`
			Attempt int    `json:"attempt"`
		}
		rows := make([]row, 0, len(all))
		for _, a := range all {
			rows = append(rows, row{a.IssueKey, string(a.State), a.Attempt})
		}
		emit(map[string]any{"assignments": rows, "count": len(rows)})
	default:
		fmt.Fprintf(os.Stderr, "cwv2 assignment: unknown subcommand %q\n\n", sub)
		assignmentUsage()
		return 2
	}
	return 0
}
