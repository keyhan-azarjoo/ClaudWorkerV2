package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"claudworker/internal/config"
	"claudworker/internal/enginehome"
	"claudworker/internal/knowledge"
)

func knowledgeUsage() {
	fmt.Fprint(os.Stderr, `cwv2 knowledge — inspect/edit the Knowledge Brain (deterministic, zero tokens; JSON output)

subcommands (all require --config <cwv2.yaml>):
  list                                       current entry for every id
  get       --id ID                          current entry for one id
  history   --id ID                          every version of one id (append-only history)
  categories                                 live category vocabulary
  add       --id ID --category C --title T --body B --source S [--status active]
  propose   --id ID --category C --title T --body B --source S   (creates a Draft for approval)
  update    --id ID [--category C] [--title T] [--body B] [--source S] [--status S]
  deprecate --id ID
  archive   --id ID
  restore   --id ID
  context   [--keywords "a,b"] [--ctx "x,y"] [--categories "c,d"] [--max-entries N] [--max-bytes N] [--include-nonactive] [--render]
  growth    [same selector flags as context]

sources: human architecture acp documentation code plugin migration
status:  active deprecated archived draft
`)
}

// cmdKnowledge exposes the S4 Knowledge Brain. All writes go through the deterministic Brain; no AI,
// no tokens. Output is JSON for machine consumption (matching the git/jira/assignment toolbelts).
func cmdKnowledge(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		knowledgeUsage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	sub := args[0]
	fs := flag.NewFlagSet("knowledge "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	id := fs.String("id", "", "entry id")
	category := fs.String("category", "", "category (documented vocabulary; any non-empty string)")
	title := fs.String("title", "", "title")
	body := fs.String("body", "", "body text")
	source := fs.String("source", "", "source: human|architecture|acp|documentation|code|plugin|migration")
	status := fs.String("status", "", "status: active|deprecated|archived|draft")
	keywords := fs.String("keywords", "", "comma-separated relevance keywords (context/growth)")
	ctx := fs.String("ctx", "", "comma-separated explicit project context (context/growth)")
	categories := fs.String("categories", "", "comma-separated include-only category filter")
	maxEntries := fs.Int("max-entries", 0, "max entries in the prompt slice (0 = unlimited)")
	maxBytes := fs.Int("max-bytes", 0, "max rendered bytes in the prompt slice (0 = unlimited)")
	includeNonActive := fs.Bool("include-nonactive", false, "include non-active entries in selection")
	render := fs.Bool("render", false, "render the prompt slice as text (context only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 knowledge: --config is required")
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(err)
	}
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	store, err := knowledge.NewFileStore(l.KnowledgeEntries)
	if err != nil {
		return emitErr(err)
	}
	b := knowledge.New(store)

	sel := knowledge.Selector{
		Keywords:         splitList(*keywords),
		Context:          splitList(*ctx),
		Categories:       splitList(*categories),
		IncludeNonActive: *includeNonActive,
		MaxEntries:       *maxEntries,
		MaxBytes:         *maxBytes,
	}

	switch sub {
	case "list":
		all, err := b.List()
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"entries": all, "count": len(all)})
	case "get":
		if *id == "" {
			return emitErr(fmt.Errorf("--id is required"))
		}
		e, ok, err := b.Get(*id)
		if err != nil {
			return emitErr(err)
		}
		if !ok {
			return emitErr(fmt.Errorf("knowledge %q: not found", *id))
		}
		emit(e)
	case "history":
		if *id == "" {
			return emitErr(fmt.Errorf("--id is required"))
		}
		hist, ok, err := b.History(*id)
		if err != nil {
			return emitErr(err)
		}
		if !ok {
			return emitErr(fmt.Errorf("knowledge %q: not found", *id))
		}
		emit(map[string]any{"id": *id, "versions": hist, "count": len(hist)})
	case "categories":
		cats, err := b.Categories()
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"categories": cats, "recommended": knowledge.RecommendedCategories})
	case "add", "propose":
		if *id == "" || *category == "" || *title == "" || *source == "" {
			return emitErr(fmt.Errorf("--id, --category, --title and --source are required"))
		}
		var e *knowledge.Entry
		if sub == "propose" {
			e, err = b.Propose(*id, *category, *title, *body, knowledge.Source(*source))
		} else {
			st := knowledge.Status(*status)
			if *status == "" {
				st = knowledge.StatusActive
			}
			e, err = b.Create(*id, *category, *title, *body, knowledge.Source(*source), st)
		}
		if err != nil {
			return emitErr(err)
		}
		emit(e)
	case "update":
		if *id == "" {
			return emitErr(fmt.Errorf("--id is required"))
		}
		ch := knowledge.Change{}
		if isSet(fs, "category") {
			ch.Category = category
		}
		if isSet(fs, "title") {
			ch.Title = title
		}
		if isSet(fs, "body") {
			ch.Body = body
		}
		if isSet(fs, "source") {
			s := knowledge.Source(*source)
			ch.Source = &s
		}
		if isSet(fs, "status") {
			s := knowledge.Status(*status)
			ch.Status = &s
		}
		e, err := b.Update(*id, ch)
		if err != nil {
			return emitErr(err)
		}
		emit(e)
	case "deprecate", "archive", "restore":
		if *id == "" {
			return emitErr(fmt.Errorf("--id is required"))
		}
		var e *knowledge.Entry
		switch sub {
		case "deprecate":
			e, err = b.Deprecate(*id)
		case "archive":
			e, err = b.Archive(*id)
		case "restore":
			e, err = b.Restore(*id)
		}
		if err != nil {
			return emitErr(err)
		}
		emit(e)
	case "context":
		selected, err := b.SelectContext(sel)
		if err != nil {
			return emitErr(err)
		}
		if *render {
			emit(map[string]any{"entries": selected, "count": len(selected), "prompt": knowledge.RenderContext(selected)})
		} else {
			emit(map[string]any{"entries": selected, "count": len(selected)})
		}
	case "growth":
		st, err := b.Growth(sel)
		if err != nil {
			return emitErr(err)
		}
		emit(st)
	default:
		fmt.Fprintf(os.Stderr, "cwv2 knowledge: unknown subcommand %q\n\n", sub)
		knowledgeUsage()
		return 2
	}
	return 0
}

// splitList parses a comma-separated flag into a trimmed, non-empty slice.
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// isSet reports whether a flag was explicitly provided (so partial Update distinguishes "clear" from
// "leave unchanged").
func isSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
