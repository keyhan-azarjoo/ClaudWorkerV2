package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"claudworker/internal/aiworkspace"
	"claudworker/internal/controlplane"
)

// registerAIWorkspace wires the AI Workspace subsystem onto the Control Plane. Phase 1 = Providers +
// Dashboard + local Usage. Handlers are thin adapters that delegate to the service (no business logic
// here), matching the repos.*/rules.* convention. API keys are stored in the OS keychain by the service;
// nothing here ever returns a raw key to the client (list responses are masked).
func registerAIWorkspace(cp *controlplane.Server, projectDir string) {
	if cp == nil || projectDir == "" {
		return
	}
	svc := aiworkspace.New(projectDir)

	// --- Queries ---
	cp.Query("aiw.dashboard.summary", func(context.Context, url.Values) (any, error) {
		return svc.Dashboard(), nil
	})
	cp.Query("aiw.providers.list", func(context.Context, url.Values) (any, error) {
		return svc.ProvidersPublic(), nil
	})
	cp.Query("aiw.provider.kinds", func(context.Context, url.Values) (any, error) {
		return svc.Kinds(), nil
	})
	cp.Query("aiw.usage.summary", func(context.Context, url.Values) (any, error) {
		return svc.UsageSummary(), nil
	})
	cp.Query("aiw.optimizers.list", func(context.Context, url.Values) (any, error) {
		return svc.OptimizersList(), nil
	})
	cp.Query("aiw.cache.stats", func(context.Context, url.Values) (any, error) {
		return svc.CacheStats(), nil
	})
	cp.Query("aiw.cache.list", func(_ context.Context, q url.Values) (any, error) {
		limit := 100
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
			limit = n
		}
		return svc.CacheList(limit), nil
	})
	cp.Query("aiw.usage.series", func(_ context.Context, q url.Values) (any, error) {
		days, _ := strconv.Atoi(q.Get("range"))
		return svc.UsageSeries(days), nil
	})
	cp.Query("aiw.workspaces.list", func(context.Context, url.Values) (any, error) {
		return svc.WorkspacesList(), nil
	})
	cp.Query("aiw.context.list", func(context.Context, url.Values) (any, error) {
		return svc.ContextList(), nil
	})
	cp.Query("aiw.context.get", func(_ context.Context, q url.Values) (any, error) {
		pack, content, ok := svc.ContextGet(q.Get("id"))
		if !ok {
			return nil, fmt.Errorf("unknown context pack")
		}
		return map[string]any{"pack": pack, "content": content}, nil
	})
	cp.Query("aiw.knowledge.list", func(_ context.Context, q url.Values) (any, error) {
		return map[string]any{"items": svc.KnowledgeList(q.Get("q")), "collections": svc.KnowledgeCollections()}, nil
	})
	cp.Query("aiw.knowledge.get", func(_ context.Context, q url.Values) (any, error) {
		item, content, ok := svc.KnowledgeGet(q.Get("id"))
		if !ok {
			return nil, fmt.Errorf("unknown knowledge item")
		}
		return map[string]any{"item": item, "content": content}, nil
	})
	cp.Query("aiw.companion.status", func(context.Context, url.Values) (any, error) {
		return svc.CompanionStatus(), nil
	})

	// --- Commands (providers) ---
	cp.Command("aiw.provider.add", func(_ context.Context, body []byte) (any, error) {
		var r struct{ Kind, Name, BaseURL string }
		_ = json.Unmarshal(body, &r)
		p, err := svc.AddProvider(r.Kind, r.Name, r.BaseURL)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": p.ID, "providers": svc.ProvidersPublic()}, nil
	})
	cp.Command("aiw.provider.update", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			BaseURL  string `json:"baseURL"`
			Priority int    `json:"priority"`
		}
		r.Priority = -1
		_ = json.Unmarshal(body, &r)
		return svc.UpdateProvider(r.ID, r.Name, r.BaseURL, r.Priority)
	})
	cp.Command("aiw.provider.remove", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.RemoveProvider(r.ID), nil
	})
	cp.Command("aiw.provider.setDefault", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.SetDefault(r.ID), nil
	})
	cp.Command("aiw.provider.enable", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.SetEnabled(r.ID, r.Enabled), nil
	})
	cp.Command("aiw.provider.test", func(ctx context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.TestProvider(ctx, r.ID)
	})

	// --- Commands (accounts / keys → keychain) ---
	cp.Command("aiw.account.add", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ProviderID string `json:"providerId"`
			Label      string `json:"label"`
			Org        string `json:"org"`
			Key        string `json:"key"`
			Model      string `json:"model"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.AddAccount(r.ProviderID, r.Label, r.Org, r.Key, r.Model)
	})
	cp.Command("aiw.account.update", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ProviderID string `json:"providerId"`
			AccountID  string `json:"accountId"`
			Label      string `json:"label"`
			Org        string `json:"org"`
			Key        string `json:"key"`
			Model      string `json:"model"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.UpdateAccount(r.ProviderID, r.AccountID, r.Label, r.Org, r.Key, r.Model)
	})
	cp.Command("aiw.account.remove", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ProviderID string `json:"providerId"`
			AccountID  string `json:"accountId"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.RemoveAccount(r.ProviderID, r.AccountID)
	})

	// --- Commands (optimizers) ---
	cp.Command("aiw.optimizer.enable", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.SetOptimizerEnabled(r.ID, r.Enabled), nil
	})
	cp.Command("aiw.optimizer.configure", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID     string         `json:"id"`
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.ConfigureOptimizer(r.ID, r.Config)
	})
	cp.Command("aiw.optimizer.run", func(ctx context.Context, body []byte) (any, error) {
		var r struct {
			ID      string         `json:"id"`
			Kind    string         `json:"kind"`
			Content string         `json:"content"`
			Config  map[string]any `json:"config"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.RunOptimizer(ctx, r.ID, r.Kind, r.Content, r.Config)
	})

	// --- Commands (cache) ---
	cp.Command("aiw.cache.clear", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(body, &r)
		return map[string]any{"removed": svc.CacheClear(r.Kind), "stats": svc.CacheStats()}, nil
	})
	cp.Command("aiw.cache.pin", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Key    string `json:"key"`
			Pinned bool   `json:"pinned"`
		}
		_ = json.Unmarshal(body, &r)
		svc.CachePin(r.Key, r.Pinned)
		return map[string]any{"ok": true}, nil
	})
	cp.Command("aiw.cache.expire", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(body, &r)
		svc.CacheExpire(r.Key)
		return map[string]any{"ok": true}, nil
	})

	// --- Commands (workspaces) ---
	cp.Command("aiw.workspace.add", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &r)
		w, err := svc.AddWorkspace(r.Name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": w.ID, "workspaces": svc.WorkspacesList()}, nil
	})
	cp.Command("aiw.workspace.update", func(_ context.Context, body []byte) (any, error) {
		var w aiworkspace.Workspace
		_ = json.Unmarshal(body, &w)
		return svc.UpdateWorkspace(w)
	})
	cp.Command("aiw.workspace.remove", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.RemoveWorkspace(r.ID), nil
	})
	cp.Command("aiw.workspace.setCurrent", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.SetCurrentWorkspace(r.ID), nil
	})

	// --- Commands (context packs) ---
	cp.Command("aiw.context.build", func(ctx context.Context, body []byte) (any, error) {
		var r struct {
			ID         string   `json:"id"`
			Name       string   `json:"name"`
			Sources    []string `json:"sources"`
			Inline     string   `json:"inline"`
			Optimizers []string `json:"optimizers"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.ContextBuild(ctx, r.ID, r.Name, r.Sources, r.Inline, r.Optimizers)
	})
	cp.Command("aiw.context.pin", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID     string `json:"id"`
			Pinned bool   `json:"pinned"`
		}
		_ = json.Unmarshal(body, &r)
		svc.ContextPin(r.ID, r.Pinned)
		return map[string]any{"ok": true}, nil
	})
	cp.Command("aiw.context.remove", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		svc.ContextRemove(r.ID)
		return map[string]any{"ok": true}, nil
	})

	// --- Commands (knowledge) ---
	cp.Command("aiw.knowledge.add", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Title, Kind, Collection, Content string
			Tags                             []string
		}
		_ = json.Unmarshal(body, &r)
		item, err := svc.KnowledgeAdd(r.Title, r.Kind, r.Collection, r.Tags, r.Content)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": item.ID}, nil
	})
	cp.Command("aiw.knowledge.update", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID, Title, Kind, Collection, Content string
			Tags                                 []string
			ContentSet                           bool `json:"contentSet"`
		}
		_ = json.Unmarshal(body, &r)
		_, err := svc.KnowledgeUpdate(r.ID, r.Title, r.Kind, r.Collection, r.Tags, r.Content, r.ContentSet)
		return map[string]any{"ok": err == nil}, err
	})
	cp.Command("aiw.knowledge.remove", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &r)
		svc.KnowledgeRemove(r.ID)
		return map[string]any{"ok": true}, nil
	})

	// --- Commands (companion) ---
	cp.Command("aiw.companion.connect", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.CompanionConnect(r.URL)
	})
	cp.Command("aiw.companion.disconnect", func(_ context.Context, _ []byte) (any, error) {
		svc.CompanionDisconnect()
		return map[string]any{"ok": true}, nil
	})

	// --- Scan (real on-disk files) ---
	cp.Command("aiw.scan.run", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Roots      []string `json:"roots"`
			Types      []string `json:"types"`
			Workspaces bool     `json:"workspaces"`
		}
		_ = json.Unmarshal(body, &r)
		if r.Workspaces {
			return svc.ScanWorkspaces(r.Types), nil
		}
		return svc.Scan(r.Roots, r.Types), nil
	})
	cp.Command("aiw.scan.optimize", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Paths []string `json:"paths"`
		}
		_ = json.Unmarshal(body, &r)
		return map[string]any{"results": svc.ScanOptimize(r.Paths)}, nil
	})
	cp.Command("aiw.scan.preview", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(body, &r)
		return svc.ScanPreview(r.Path)
	})
	cp.Command("aiw.scan.restore", func(_ context.Context, body []byte) (any, error) {
		var r struct {
			Path string `json:"path"`
			All  bool   `json:"all"`
		}
		_ = json.Unmarshal(body, &r)
		if r.All {
			return map[string]any{"restored": svc.ScanRestoreAll()}, nil
		}
		if err := svc.ScanRestore(r.Path); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	})
	cp.Query("aiw.scan.backups", func(context.Context, url.Values) (any, error) {
		return svc.ScanBackups(), nil
	})
}
