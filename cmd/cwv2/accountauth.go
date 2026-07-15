package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"claudworker/internal/config"
)

// accountAuth manages REAL login status + interactive login/logout for worker accounts (V1 parity).
//
// The Accounts page used to show only the resource layer's health/availability, which has nothing to do
// with whether the account's CLI is actually signed in — so an account could read "healthy/available"
// while its CLI was logged out, and jobs then failed with "not logged in". This queries the actual CLI
// auth state per account and drives the OAuth paste-code login + logout from the console.
type accountAuth struct {
	accounts  []config.Account
	claudeBin string
	codexBin  string

	mu      sync.Mutex
	pending map[string]*pendingLogin // keyed by account name

	cacheMu sync.Mutex
	cache   map[string]any
	cacheAt time.Time
}

// pendingLogin holds an in-progress OAuth login (the CLI process waiting for the pasted code on stdin).
type pendingLogin struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	urlCh  chan string
	doneCh chan struct{}

	mu   sync.Mutex
	out  []string
	url  string
	done bool
	err  error
}

func newAccountAuth(accounts []config.Account, claudeBin string) *accountAuth {
	if claudeBin == "" {
		claudeBin = "claude"
	}
	return &accountAuth{accounts: accounts, claudeBin: claudeBin, codexBin: "codex", pending: map[string]*pendingLogin{}}
}

func expandHome(dir string) string {
	if strings.HasPrefix(dir, "~/") {
		if home, _ := os.UserHomeDir(); home != "" {
			return home + dir[1:]
		}
	}
	return dir
}

func (a *accountAuth) find(name string) (config.Account, bool) {
	for _, ac := range a.accounts {
		if ac.Name == name {
			return ac, true
		}
	}
	return config.Account{}, false
}

func engineOf(ac config.Account) string {
	if ac.Engine == "codex" {
		return "codex"
	}
	return "claude"
}

// status returns one account's REAL login state by invoking the CLI's status command in that account's
// config dir. Never returns secrets — just booleans/labels.
func (a *accountAuth) status(ac config.Account) map[string]any {
	dir := expandHome(ac.ConfigDir)
	eng := engineOf(ac)
	res := map[string]any{"name": ac.Name, "engine": eng, "loggedIn": false, "configDir": dir}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	if eng == "codex" {
		cmd := exec.CommandContext(ctx, a.codexBin, "login", "status")
		cmd.Env = append(os.Environ(), "CODEX_HOME="+dir)
		out, _ := cmd.CombinedOutput()
		line := strings.TrimSpace(firstLineS(string(out)))
		res["loggedIn"] = strings.Contains(strings.ToLower(line), "logged in")
		res["detail"] = line
		return res
	}
	cmd := exec.CommandContext(ctx, a.claudeBin, "auth", "status", "--json")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	out, _ := cmd.Output()
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if json.Unmarshal(out, &st) == nil {
		res["loggedIn"] = st.LoggedIn
		res["detail"] = st.AuthMethod
	}
	return res
}

// statusAll returns every account's login state (probed concurrently), cached ~20s so the page's 15s
// refresh doesn't spawn CLIs constantly.
func (a *accountAuth) statusAll() map[string]any {
	a.cacheMu.Lock()
	if a.cache != nil && time.Since(a.cacheAt) < 20*time.Second {
		c := a.cache
		a.cacheMu.Unlock()
		return c
	}
	a.cacheMu.Unlock()

	out := map[string]any{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, ac := range a.accounts {
		wg.Add(1)
		go func(ac config.Account) {
			defer wg.Done()
			s := a.status(ac)
			mu.Lock()
			out[ac.Name] = s
			mu.Unlock()
		}(ac)
	}
	wg.Wait()

	a.cacheMu.Lock()
	a.cache = out
	a.cacheAt = time.Now()
	a.cacheMu.Unlock()
	return out
}

func (a *accountAuth) invalidate() {
	a.cacheMu.Lock()
	a.cache = nil
	a.cacheMu.Unlock()
}

var loginURLRe = regexp.MustCompile(`https?://\S+`)

// beginLogin starts the account's OAuth login and returns the URL for the user to visit. The CLI process
// stays alive waiting for the pasted code (submitted via submitCode). Claude uses a paste-code flow;
// codex uses a device/callback flow (still prints a URL to visit).
func (a *accountAuth) beginLogin(name string) (map[string]any, error) {
	ac, ok := a.find(name)
	if !ok {
		return nil, fmt.Errorf("unknown account %q", name)
	}
	// Already logged in? Don't spawn a login.
	if a.status(ac)["loggedIn"] == true {
		return map[string]any{"name": name, "alreadyLoggedIn": true}, nil
	}
	eng := engineOf(ac)
	dir := expandHome(ac.ConfigDir)

	a.mu.Lock()
	if prev := a.pending[name]; prev != nil {
		prev.kill()
	}
	var cmd *exec.Cmd
	if eng == "codex" {
		cmd = exec.Command(a.codexBin, "login")
		cmd.Env = append(os.Environ(), "CODEX_HOME="+dir)
	} else {
		cmd = exec.Command(a.claudeBin, "auth", "login", "--claudeai")
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir, "CLAUDE_DISABLE_BROWSER_LOGIN=1")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	p := &pendingLogin{cmd: cmd, stdin: stdin, urlCh: make(chan string, 1), doneCh: make(chan struct{})}
	a.pending[name] = p
	a.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start login: %w", err)
	}
	// Reaper: wait for exit, then close the pipe so the reader sees EOF.
	go func() {
		werr := cmd.Wait()
		p.mu.Lock()
		p.done = true
		p.err = werr
		p.mu.Unlock()
		_ = pw.Close()
		close(p.doneCh)
	}()
	// Reader: capture output, extract the login URL.
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			p.mu.Lock()
			p.out = append(p.out, line)
			gotURL := p.url == ""
			p.mu.Unlock()
			if gotURL {
				if u := loginURLRe.FindString(line); u != "" && strings.Contains(u, "oauth") {
					p.mu.Lock()
					p.url = u
					p.mu.Unlock()
					select {
					case p.urlCh <- u:
					default:
					}
				}
			}
		}
	}()

	select {
	case u := <-p.urlCh:
		return map[string]any{"name": name, "engine": eng, "url": u}, nil
	case <-p.doneCh:
		return map[string]any{"name": name, "engine": eng, "note": "login exited before a URL appeared: " + p.tail()}, nil
	case <-time.After(25 * time.Second):
		return map[string]any{"name": name, "engine": eng, "note": "no login URL was produced — try again, or run it on the host"}, nil
	}
}

// submitCode sends the pasted OAuth code to the waiting login process and reports the result.
func (a *accountAuth) submitCode(name, code string) (map[string]any, error) {
	a.mu.Lock()
	p := a.pending[name]
	a.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("no login in progress for %q — click Login first", name)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("paste the code from the login page")
	}
	if _, err := io.WriteString(p.stdin, code+"\n"); err != nil {
		return nil, fmt.Errorf("could not send the code: %w", err)
	}
	select {
	case <-p.doneCh:
	case <-time.After(45 * time.Second):
		return map[string]any{"ok": false, "message": "login timed out waiting to complete"}, nil
	}
	a.mu.Lock()
	delete(a.pending, name)
	a.mu.Unlock()
	a.invalidate()

	ac, _ := a.find(name)
	if a.status(ac)["loggedIn"] == true {
		return map[string]any{"ok": true, "loggedIn": true}, nil
	}
	msg := "login did not complete"
	p.mu.Lock()
	if p.err != nil {
		msg = p.err.Error()
	}
	p.mu.Unlock()
	return map[string]any{"ok": false, "loggedIn": false, "message": msg + " · " + p.tail()}, nil
}

// cancelLogin aborts an in-progress login.
func (a *accountAuth) cancelLogin(name string) {
	a.mu.Lock()
	p := a.pending[name]
	delete(a.pending, name)
	a.mu.Unlock()
	if p != nil {
		p.kill()
	}
}

// logout signs the account out via the CLI's own logout command.
func (a *accountAuth) logout(name string) (map[string]any, error) {
	ac, ok := a.find(name)
	if !ok {
		return nil, fmt.Errorf("unknown account %q", name)
	}
	dir := expandHome(ac.ConfigDir)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if engineOf(ac) == "codex" {
		cmd = exec.CommandContext(ctx, a.codexBin, "logout")
		cmd.Env = append(os.Environ(), "CODEX_HOME="+dir)
	} else {
		cmd = exec.CommandContext(ctx, a.claudeBin, "auth", "logout")
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	}
	out, err := cmd.CombinedOutput()
	a.invalidate()
	if err != nil {
		return map[string]any{"ok": false, "message": strings.TrimSpace(firstLineS(string(out)))}, nil
	}
	return map[string]any{"ok": true}, nil
}

func (p *pendingLogin) kill() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.stdin.Close()
}

// tail returns the last few output lines (for surfacing an error to the UI).
func (p *pendingLogin) tail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.out)
	if n == 0 {
		return ""
	}
	start := n - 3
	if start < 0 {
		start = 0
	}
	return strings.TrimSpace(strings.Join(p.out[start:], " · "))
}

func firstLineS(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
