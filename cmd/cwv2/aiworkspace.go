package main

import (
	"context"
	"encoding/json"
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
}
