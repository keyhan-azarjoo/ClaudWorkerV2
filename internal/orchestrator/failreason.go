package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// simpleFailReason turns the technical failure + the agent's own output into ONE plain sentence a
// non-engineer can read and act on. It scans the task's output for well-known failure signatures and
// falls back to the raw reason. Order matters: most specific / most common first.
func simpleFailReason(streamText, technicalReason string) string {
	t := strings.ToLower(streamText + "\n" + technicalReason)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("oauth", " 401", "invalid authentication", "not logged in", "failed to authenticate", "session expired", "could not be refreshed"):
		return "The account wasn't logged in. Open Accounts, sign it in, then Continue."
	case has("session limit", "usage limit", "rate limit", "quota", "resets at", "hit your"):
		return "The account hit its usage limit. Continue on another account, or wait for it to reset."
	case has("pi is offline", "device offline", "power-cycle", "physically", "no physical access", "board is offline", "offline hub", "on-site", "power down"):
		return "This needs a physical device that's offline — the AI can't fix it remotely. Someone has to power-cycle/check the device."
	case has("no esp32", "no serial device", "no usable serial", "flash", "prove real hardware", "firmware source", "firmware artifact"):
		return "This is a firmware/hardware task, but the firmware code and a device aren't in this workspace. Grant the firmware repo on the Access page, or run it where the device is."
	case has("outside my sandbox", "outside the sandbox", "not in this repo", "backend-only", "does not contain the app", "other repos", "plan doc is outside"):
		return "The task needs other repos/files that aren't in this workspace. Grant them on the Access page, or split the ticket per repo."
	case has("merge conflict", "overlaps changes", "rebase"):
		return "The change clashed with other work (merge conflict). Open it and Continue with 'rebase onto latest'."
	case has("build fail", "verification", "test failed", "tests failed", "compile", "did not pass"):
		return "The change didn't pass the build/verification. Open it and Continue to fix the build."
	case has("no code change", "no changes were made", "\"files\":[]", "nothing to merge", "ok\":false", "could not complete", "not complete"):
		return "The agent couldn't finish it and made no code change, so there was nothing to merge."
	case has("deactivated", "no active repo"):
		return "The project is turned off (no active repo on the Git page). Activate a repo, then run it."
	}
	if r := strings.TrimSpace(technicalReason); r != "" {
		return r
	}
	return "The task did not complete."
}

// lastRunSegment returns only the FINAL run's output from a task log (a retried task's log holds several
// runs, each starting with "▶ agent started"). Classifying the last run avoids picking up a stale signal
// from an earlier attempt (e.g. an early auth failure that was later fixed).
func lastRunSegment(text string) string {
	if i := strings.LastIndex(text, "▶ agent started"); i >= 0 {
		return text[i:]
	}
	return text
}

// accessResourceRe matches a REAL local path (~/… or /…) or an http(s) URL, with no spaces/quotes/angle
// brackets — so a placeholder like "/<the firmware repo>" does NOT match (we then leave it blank for the
// operator to fill).
var accessResourceRe = regexp.MustCompile(`(~?/[^\s"'<>]+|https?://[^\s"'<>]+)`)

// parseAccessRequest pulls a CLEAN resource (path/URL, or "" if only a placeholder) and the human "why"
// out of the text following an "ACCESS-REQUEST:" marker (which may be embedded in a JSON result line).
func parseAccessRequest(after string) (resource, detail string) {
	s := after
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] // first line only
	}
	if i := strings.IndexByte(s, '"'); i >= 0 {
		s = s[:i] // stop at JSON string junk (the marker has no quotes)
	}
	s = strings.TrimSpace(s)
	pathPart := s
	for _, sep := range []string{" — ", " – ", " -- ", " - "} { // path — why
		if i := strings.Index(s, sep); i >= 0 {
			pathPart = strings.TrimSpace(s[:i])
			detail = strings.TrimSpace(s[i+len(sep):])
			break
		}
	}
	resource = accessResourceRe.FindString(pathPart) // "" if it was only a placeholder like /<...>
	return resource, detail
}

// accessNeeded reports whether a failure was caused by the worker lacking access to something outside
// its worktree (a repo/folder/plan doc), plus a CLEAN resource hint (path/URL, may be empty) and the
// agent's "why". It honors an explicit "ACCESS-REQUEST: …" line, then falls back to known signatures.
func accessNeeded(streamText string) (need bool, resource, detail string) {
	if i := strings.Index(streamText, "ACCESS-REQUEST:"); i >= 0 {
		res, why := parseAccessRequest(streamText[i+len("ACCESS-REQUEST:"):])
		return true, res, why
	}
	t := strings.ToLower(streamText)
	for _, sig := range []string{
		"outside my sandbox", "outside the sandbox", "not in this repo", "not in this worktree",
		"backend-only", "does not contain the app", "other repos", "another repo",
		"firmware source", "firmware artifact", "no esp32", "no serial device",
		"not available in this worktree", "plan doc is outside", "isn't in this backend", "outside this repo",
	} {
		if strings.Contains(t, sig) {
			return true, "", "" // resource unknown → the operator specifies the folder/repo on Allow
		}
	}
	return false, "", ""
}

// FailReason returns the plain-language reason a task failed. It uses the reason computed at fail time
// (in memory) and, after a restart, recomputes it from the persisted task log so it survives restarts.
func (o *Orchestrator) FailReason(issue string) string {
	o.mu.Lock()
	r := o.failReasons[issue]
	o.mu.Unlock()
	if r != "" {
		return r
	}
	if o.TaskLogDir != "" {
		if b, err := os.ReadFile(filepath.Join(o.TaskLogDir, logSlug(issue)+".log")); err == nil {
			return simpleFailReason(lastRunSegment(string(b)), "")
		}
	}
	return ""
}
