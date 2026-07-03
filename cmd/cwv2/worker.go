package main

import (
	"flag"
	"fmt"
	"os"

	"claudworker/internal/assignment"
	"claudworker/internal/runtime"
)

func workerUsage() {
	fmt.Fprint(os.Stderr, `cwv2 worker — Worker Runtime inspection (JSON output)

subcommands:
  prompt   render the deterministic prompt + estimated size/token metrics (ZERO tokens; no provider run)
             --issue KEY --summary S --ac "criteria" [--knowledge TEXT] [--file PATH ...]

Live provider execution (claude -p) is driven by the engine's run loop, not this command, so
inspecting a prompt never spends tokens.
`)
}

// cmdWorker exposes zero-token inspection of the Worker Runtime. It renders the exact prompt a
// provider would receive (built from the four permitted inputs only) and the estimated metrics,
// without ever launching a provider — so it is safe and spends no tokens.
func cmdWorker(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		workerUsage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	sub := args[0]
	if sub != "prompt" {
		fmt.Fprintf(os.Stderr, "cwv2 worker: unknown subcommand %q\n\n", sub)
		workerUsage()
		return 2
	}

	fs := flag.NewFlagSet("worker prompt", flag.ContinueOnError)
	issue := fs.String("issue", "", "issue key (required)")
	summary := fs.String("summary", "", "task summary")
	ac := fs.String("ac", "", "acceptance criteria")
	knowledge := fs.String("knowledge", "", "knowledge context (as produced by the Knowledge Brain)")
	var files multiFlag
	fs.Var(&files, "file", "path to a relevant file to include (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *issue == "" {
		return emitErr(fmt.Errorf("--issue is required"))
	}

	in := assignment.WorkerInput{
		IssueKey:           *issue,
		Summary:            *summary,
		AcceptanceCriteria: *ac,
		KnowledgeContext:   *knowledge,
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			return emitErr(err)
		}
		in.RelevantFiles = append(in.RelevantFiles, assignment.File{Path: p, Content: string(b)})
	}

	prompt := runtime.BuildPrompt(in)
	emit(map[string]any{
		"prompt":         prompt,
		"prompt_bytes":   len(prompt),
		"token_estimate": runtime.EstimateTokens(len(prompt)),
		"relevant_files": len(in.RelevantFiles),
	})
	return 0
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprintf("%v", []string(*m)) }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
