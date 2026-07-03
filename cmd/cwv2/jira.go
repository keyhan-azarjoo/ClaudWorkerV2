package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"claudworker/internal/config"
	"claudworker/internal/jira"
	"claudworker/internal/logging"
	"claudworker/internal/secrets"
)

func jiraUsage() {
	fmt.Fprint(os.Stderr, `cwv2 jira — deterministic Jira toolbelt (JSON output)

all subcommands require --config <cwv2.yaml> (for base URL + auth secret names):
  health
  search --jql Q [--max N]
  get --key K
  transitions --key K
  transition --key K --to "Done"
  comments --key K
  comment --key K --body "..."
  labels-add --key K --labels a,b
  automation-get --key K
  automation-set --key K --value "Enabled"      (Enabled|Disabled|Manual Only|Needs Review)
  ac --key K
`)
}

// jiraClientFromConfig builds a Jira client using base_url from config and auth token resolved from
// the vault by NAME (never embedded in config). Returns a clear error if auth cannot be resolved.
func jiraClientFromConfig(cfg *config.Config, verbose bool) (*jira.Client, error) {
	r := secrets.NewResolver()
	email, err := r.Resolve(cfg.Jira.Auth.UserSecret)
	if err != nil {
		return nil, fmt.Errorf("resolve jira user secret %q: %w", cfg.Jira.Auth.UserSecret, err)
	}
	token, err := r.Resolve(cfg.Jira.Auth.TokenSecret)
	if err != nil {
		return nil, fmt.Errorf("resolve jira token secret %q: %w", cfg.Jira.Auth.TokenSecret, err)
	}
	opts := []jira.Option{}
	if cfg.Jira.AutomationField != "" {
		opts = append(opts, jira.WithAutomationField(cfg.Jira.AutomationField))
	}
	if verbose {
		opts = append(opts, jira.WithLogger(logging.New(os.Stderr, "info", "text")))
	}
	return jira.New(cfg.Jira.BaseURL, email, token, opts...), nil
}

func cmdJira(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		jiraUsage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	sub := args[0]
	fs := flag.NewFlagSet("jira "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	key := fs.String("key", "", "issue key")
	jql := fs.String("jql", "", "JQL query")
	max := fs.Int("max", 50, "max results")
	to := fs.String("to", "", "target status name")
	body := fs.String("body", "", "comment body")
	labels := fs.String("labels", "", "comma-separated labels")
	value := fs.String("value", "", "Automation value")
	verbose := fs.Bool("v", false, "verbose logs to stderr")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 jira: --config is required")
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(err)
	}
	c, err := jiraClientFromConfig(cfg, *verbose)
	if err != nil {
		return emitErr(err)
	}
	ctx := context.Background()

	switch sub {
	case "health":
		me, err := c.Health(ctx)
		if err != nil {
			return emitErr(err)
		}
		emit(me)
	case "search":
		res, err := c.Search(ctx, *jql, nil, *max)
		if err != nil {
			return emitErr(err)
		}
		emit(res)
	case "get":
		iss, err := c.GetIssue(ctx, *key)
		if err != nil {
			return emitErr(err)
		}
		emit(iss)
	case "transitions":
		trs, err := c.Transitions(ctx, *key)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"transitions": trs})
	case "transition":
		tr, err := c.TransitionTo(ctx, *key, *to)
		if err != nil {
			return emitErr(err)
		}
		emit(tr)
	case "comments":
		cs, err := c.Comments(ctx, *key)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"comments": cs})
	case "comment":
		cm, err := c.AddComment(ctx, *key, *body)
		if err != nil {
			return emitErr(err)
		}
		emit(cm)
	case "labels-add":
		ls := splitCSV(*labels)
		if err := c.AddLabels(ctx, *key, ls...); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"added": ls})
	case "automation-get":
		v, err := c.GetAutomation(ctx, *key)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]string{"automation": string(v)})
	case "automation-set":
		if err := c.SetAutomation(ctx, *key, jira.AutomationValue(*value)); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"automation": *value})
	case "ac":
		ac, err := c.AcceptanceCriteria(ctx, *key)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]string{"acceptance_criteria": ac})
	default:
		fmt.Fprintf(os.Stderr, "cwv2 jira: unknown subcommand %q\n\n", sub)
		jiraUsage()
		return 2
	}
	return 0
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
