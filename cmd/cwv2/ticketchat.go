package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claudworker/internal/config"
	"claudworker/internal/jira"
)

// firstClaudeConfigDir returns the CLAUDE_CONFIG_DIR of the first claude account (~ expanded), for the
// ticket-chat agent to run under.
func firstClaudeConfigDir(accounts []config.Account) string {
	home, _ := os.UserHomeDir()
	for _, a := range accounts {
		if a.Engine != "" && a.Engine != "claude" {
			continue
		}
		d := a.ConfigDir
		if strings.HasPrefix(d, "~/") && home != "" {
			d = home + d[1:]
		}
		if d != "" {
			return d
		}
	}
	return ""
}

// jiraIssueDetail returns the full detail for one ticket — summary, description (as text), status,
// priority, labels, comments and the available status transitions (so the UI can change/cancel it).
func jiraIssueDetail(ctx context.Context, cli *jira.Client, key string) (map[string]any, error) {
	if cli == nil {
		return nil, fmt.Errorf("jira not configured")
	}
	iss, err := cli.GetIssue(ctx, key)
	if err != nil {
		return nil, err
	}
	prio := ""
	if iss.Fields.Priority != nil {
		prio = iss.Fields.Priority.Name
	}
	comments := []map[string]any{}
	if cs, err := cli.Comments(ctx, key); err == nil {
		for _, c := range cs {
			comments = append(comments, map[string]any{"author": c.Author.DisplayName, "text": jira.ADFToText(c.Body)})
		}
	}
	transitions := []map[string]any{}
	if ts, err := cli.Transitions(ctx, key); err == nil {
		for _, t := range ts {
			transitions = append(transitions, map[string]any{"name": t.To.Name, "transition": t.Name})
		}
	}
	return map[string]any{
		"key": iss.Key, "summary": iss.Fields.Summary, "description": jira.ADFToText(iss.Fields.Description),
		"status": iss.Fields.Status.Name, "priority": prio, "labels": iss.Fields.Labels,
		"comments": comments, "transitions": transitions,
	}, nil
}

// chatMsg is one turn of a per-ticket conversation.
type chatMsg struct {
	Role    string `json:"role"` // "user" | "agent"
	Text    string `json:"text"`
	At      string `json:"at"`
	Mode    string `json:"mode,omitempty"`    // "explain" | "investigate"
	Pending bool   `json:"pending,omitempty"` // agent turn still running
	Saved   bool   `json:"saved_to_jira,omitempty"`
}

// ticketChat runs a lightweight, per-ticket AI conversation. By default it EXPLAINS the ticket using
// only its text (fast, no code access). In "investigate" mode it examines the codebase and posts a
// concise summary back to the Jira ticket. Conversations are persisted per ticket so the operator can
// leave and come back for the result (the agent runs in the background).
type ticketChat struct {
	dir       string // <ProjectDir>/ticket-chats
	jira      *jira.Client
	repoDir   string
	claudeBin string
	configDir string // CLAUDE_CONFIG_DIR of a claude account
	mu        sync.Mutex
	running   map[string]bool
}

func newTicketChat(projectDir, repoDir, claudeBin, configDir string, jc *jira.Client) *ticketChat {
	return &ticketChat{dir: filepath.Join(projectDir, "ticket-chats"), jira: jc, repoDir: repoDir, claudeBin: claudeBin, configDir: configDir, running: map[string]bool{}}
}

func (t *ticketChat) file(key string) string { return filepath.Join(t.dir, slugify(key)+".json") }

func (t *ticketChat) load(key string) []chatMsg {
	b, err := os.ReadFile(t.file(key))
	if err != nil {
		return nil
	}
	var out []chatMsg
	_ = json.Unmarshal(b, &out)
	return out
}

func (t *ticketChat) save(key string, msgs []chatMsg) {
	_ = os.MkdirAll(t.dir, 0o755)
	if b, err := json.MarshalIndent(msgs, "", "  "); err == nil {
		_ = os.WriteFile(t.file(key), b, 0o644)
	}
}

// conversation returns the stored turns for a ticket (oldest first).
func (t *ticketChat) conversation(key string) []chatMsg {
	t.mu.Lock()
	defer t.mu.Unlock()
	msgs := t.load(key)
	if msgs == nil {
		return []chatMsg{}
	}
	return msgs
}

// send appends the user turn + a pending agent turn, then runs the agent in the BACKGROUND. Returns
// immediately so the UI can poll conversation().
func (t *ticketChat) send(key, message string, investigate bool) error {
	if t.claudeBin == "" {
		return fmt.Errorf("no claude binary configured")
	}
	mode := "explain"
	if investigate {
		mode = "investigate"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	t.mu.Lock()
	if t.running[key] {
		t.mu.Unlock()
		return fmt.Errorf("the agent is still working on the previous message for %s", key)
	}
	msgs := t.load(key)
	msgs = append(msgs, chatMsg{Role: "user", Text: strings.TrimSpace(message), At: now, Mode: mode})
	msgs = append(msgs, chatMsg{Role: "agent", Text: "", At: now, Mode: mode, Pending: true})
	t.save(key, msgs)
	t.running[key] = true
	t.mu.Unlock()

	go t.run(key, message, investigate)
	return nil
}

func (t *ticketChat) run(key, message string, investigate bool) {
	defer func() {
		t.mu.Lock()
		delete(t.running, key)
		t.mu.Unlock()
	}()
	timeout := 90 * time.Second
	if investigate {
		timeout = 8 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	iss, err := t.jira.GetIssue(ctx, key)
	if err != nil {
		t.finish(key, "", fmt.Errorf("could not load ticket: %w", err), false)
		return
	}
	desc := jira.ADFToText(iss.Fields.Description)
	prompt := t.buildPrompt(key, iss.Fields.Summary, iss.Fields.Status.Name, desc, t.history(key), message, investigate)

	dir := ""
	tools := false
	if investigate {
		dir = t.repoDir
		tools = true
	}
	out, runErr := t.runClaude(ctx, prompt, dir, tools)
	saved := false
	if runErr == nil && investigate && strings.TrimSpace(out) != "" && t.jira != nil {
		if _, e := t.jira.AddComment(ctx, key, "🤖 ClaudWorker investigation\n\n"+out); e == nil {
			saved = true
		}
	}
	t.finish(key, out, runErr, saved)
}

// history renders the prior turns (bounded) for conversation context.
func (t *ticketChat) history(key string) string {
	msgs := t.load(key)
	var b strings.Builder
	start := 0
	if len(msgs) > 12 {
		start = len(msgs) - 12
	}
	for _, m := range msgs[start:] {
		if m.Pending || strings.TrimSpace(m.Text) == "" {
			continue
		}
		who := "User"
		if m.Role == "agent" {
			who = "Assistant"
		}
		fmt.Fprintf(&b, "%s: %s\n", who, m.Text)
	}
	return b.String()
}

func (t *ticketChat) buildPrompt(key, summary, status, desc, history, message string, investigate bool) string {
	var b strings.Builder
	if investigate {
		b.WriteString("You are investigating ONE Jira ticket against the codebase in your current directory. Read whatever you need to answer, but DO NOT modify any files. Answer the user's request, then END with a clear, concise summary (a few sentences) suitable to post back to the Jira ticket as a comment.\n\n")
	} else {
		b.WriteString("You are explaining ONE Jira ticket. Use ONLY the ticket text below — do NOT read the codebase or use tools. Be brief and clear (a few sentences). If the user explicitly asks you to investigate the code, say that they should use the Investigate action.\n\n")
	}
	fmt.Fprintf(&b, "# Ticket %s: %s\nStatus: %s\n\n%s\n", key, summary, status, strings.TrimSpace(desc))
	if strings.TrimSpace(history) != "" {
		b.WriteString("\n# Conversation so far\n")
		b.WriteString(strings.TrimSpace(history))
		b.WriteString("\n")
	}
	b.WriteString("\n# User\n")
	b.WriteString(strings.TrimSpace(message))
	b.WriteString("\n\nAnswer:")
	return b.String()
}

func (t *ticketChat) runClaude(ctx context.Context, prompt, dir string, tools bool) (string, error) {
	args := []string{"-p"}
	if tools {
		args = append(args, "--permission-mode", "acceptEdits")
	}
	cmd := exec.CommandContext(ctx, t.claudeBin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	if dir != "" {
		cmd.Dir = dir
	}
	env := os.Environ()
	if t.configDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+t.configDir)
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("agent error: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// finish replaces the last pending agent turn with the result (or an error).
func (t *ticketChat) finish(key, out string, runErr error, saved bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	msgs := t.load(key)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "agent" && msgs[i].Pending {
			if runErr != nil {
				msgs[i].Text = "⚠ " + runErr.Error()
			} else if strings.TrimSpace(out) == "" {
				msgs[i].Text = "(no answer)"
			} else {
				msgs[i].Text = out
			}
			msgs[i].Pending = false
			msgs[i].Saved = saved
			break
		}
	}
	t.save(key, msgs)
}
