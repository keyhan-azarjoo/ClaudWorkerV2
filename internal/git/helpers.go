package git

import (
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
)

// discardHandler is a no-op slog.Handler so a Git without a logger is safe to use.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// parsePorcelain parses `git status --porcelain` output into FileChange entries.
func parsePorcelain(out string) []FileChange {
	var changes []FileChange
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		changes = append(changes, FileChange{
			Status: line[:2],
			Path:   strings.TrimSpace(line[3:]),
		})
	}
	return changes
}

// parseWorktrees parses `git worktree list --porcelain` output.
func parseWorktrees(out string) []Worktree {
	var (
		list []Worktree
		cur  Worktree
		have bool
	)
	flush := func() {
		if have {
			list = append(list, cur)
		}
		cur, have = Worktree{}, false
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
			have = true
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			// blank line separates entries
		}
	}
	flush()
	return list
}

// sameWorktreePath compares two paths for identity, resolving symlinks (git reports the real path,
// e.g. /private/var on macOS, while callers pass /var). Falls back to absolute-clean when a path
// does not yet exist on disk.
func sameWorktreePath(a, b string) bool {
	return canonPath(a) == canonPath(b)
}

func canonPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}
