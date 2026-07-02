package verify

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// CommandVerifier is a real, deterministic verifier that runs a command and maps its exit status to
// an outcome (0 → Pass, non-zero → Fail). It covers the non-interactive verification types — unit,
// build, integration, api, documentation — by wrapping the project's own test/build/lint commands.
// It honours ctx (timeout/cancellation); a context timeout yields Inconclusive (could not determine).
type CommandVerifier struct {
	VName   string   // plugin name
	VType   Type     // verification type this instance covers
	VCaps   []string // capabilities provided (e.g. ["go","unit"])
	Command []string // argv (e.g. ["go","test","./..."])
	Dir     string   // working directory ("" = current)
	MaxLog  int      // max bytes of combined output kept in Logs (0 = 8 KiB default)
}

func (c CommandVerifier) Name() string           { return c.VName }
func (c CommandVerifier) Type() Type             { return c.VType }
func (c CommandVerifier) Capabilities() []string { return c.VCaps }

// Verify runs the command and reports the outcome.
func (c CommandVerifier) Verify(ctx context.Context, req Request) (Result, error) {
	res := Result{Metrics: map[string]float64{}}
	if len(c.Command) == 0 {
		res.Outcome = Blocked
		res.Summary = "no command configured"
		return res, nil
	}
	dir := c.Dir
	if dir == "" {
		dir = req.Target // allow the request to point the command at a repo/worktree
	}
	cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	res.Logs = tail(out.String(), c.maxLog())
	if ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled {
		res.Outcome = Inconclusive
		res.Summary = "command did not complete: " + ctx.Err().Error()
		return res, nil
	}
	if err != nil {
		res.Outcome = Fail
		res.Summary = "command failed: " + strings.Join(c.Command, " ")
		res.Detail = err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			res.Metrics["exit_code"] = float64(ee.ExitCode())
		} else {
			res.Metrics["exit_code"] = -1
		}
		return res, nil
	}
	res.Outcome = Pass
	res.Summary = "command succeeded: " + strings.Join(c.Command, " ")
	res.Metrics["exit_code"] = 0
	return res, nil
}

func (c CommandVerifier) maxLog() int {
	if c.MaxLog > 0 {
		return c.MaxLog
	}
	return 8 * 1024
}

// tail returns the last n bytes of s as a single-element log slice (empty if s is empty).
func tail(s string, n int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return []string{s}
}
