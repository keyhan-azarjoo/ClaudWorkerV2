package main

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// taskControl pauses/resumes a RUNNING task's worker processes. It finds the worker CLI (and its
// subagents) by their working directory — every process whose cwd is inside the task's worktree
// (…/worktrees/<repo>/<issue>) — the same discovery taskAgentCounts uses, then sends SIGSTOP/SIGCONT.
// Pause freezes the whole task (the pipeline blocks on the frozen CLI's output); Resume unfreezes it.
type taskControl struct {
	mu     sync.Mutex
	paused map[string]bool // issues currently paused (in-memory; processes vanish on restart anyway)
}

func newTaskControl() *taskControl { return &taskControl{paused: map[string]bool{}} }

// workerPidsForIssue returns the PIDs of worker/subagent processes running inside the issue's worktree.
func workerPidsForIssue(issue string) []int {
	issue = strings.TrimSpace(issue)
	if issue == "" {
		return nil
	}
	out, err := exec.Command("lsof", "-a", "-c", "claude", "-c", "codex", "-d", "cwd", "-Fpn").Output()
	if err != nil {
		return nil
	}
	var pids []int
	cur := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur, _ = strconv.Atoi(line[1:])
		case 'n':
			path := line[1:]
			if i := strings.Index(path, "/worktrees/"); i >= 0 && cur > 0 {
				parts := strings.Split(path[i+len("/worktrees/"):], "/")
				if len(parts) >= 2 && parts[1] == issue { // parts[0]=repo, parts[1]=issue
					pids = append(pids, cur)
				}
			}
		}
	}
	return pids
}

func signalIssue(issue string, sig syscall.Signal) int {
	n := 0
	for _, pid := range workerPidsForIssue(issue) {
		if syscall.Kill(pid, sig) == nil {
			n++
		}
	}
	return n
}

// pause freezes the task's worker processes. Returns the number signalled.
func (t *taskControl) pause(issue string) int {
	n := signalIssue(issue, syscall.SIGSTOP)
	t.mu.Lock()
	t.paused[strings.TrimSpace(issue)] = true
	t.mu.Unlock()
	return n
}

// resume unfreezes the task's worker processes.
func (t *taskControl) resume(issue string) int {
	n := signalIssue(issue, syscall.SIGCONT)
	t.mu.Lock()
	delete(t.paused, strings.TrimSpace(issue))
	t.mu.Unlock()
	return n
}

// pausedList returns the issues currently marked paused.
func (t *taskControl) pausedList() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.paused))
	for k := range t.paused {
		out = append(out, k)
	}
	return out
}
