package main

import (
	"flag"
	"os"

	"claudworker/internal/stress"
)

// cmdStress runs the deterministic stress harness (Simulation Mode) and prints the report.
func cmdStress(args []string) int {
	fs := flag.NewFlagSet("stress", flag.ContinueOnError)
	n := fs.Int("issues", 100, "number of issues to drive through the loop")
	restart := fs.Int("restart-after", 30, "simulate a crash + restart after this many claims (0 = none)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, err := os.MkdirTemp("", "cwv2-stress-*")
	if err != nil {
		return emitErr(err)
	}
	defer os.RemoveAll(dir)

	rep, err := stress.Run(dir, stress.Config{Issues: *n, RestartAfter: *restart})
	if err != nil {
		return emitErr(err)
	}
	emit(map[string]any{
		"issues": rep.Issues, "done": rep.Done, "failed": rep.Failed, "deferred": rep.Deferred,
		"terminal": rep.Terminal, "rounds": rep.Rounds, "restarted": rep.Restarted,
		"elapsed_ms": rep.Elapsed.Milliseconds(), "signature": rep.Signature,
		"deterministic_recovery": rep.Terminal == rep.Issues,
	})
	return 0
}
