package aiworkspace

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Service is the AI Workspace facade. It owns the per-project stores and exposes plain data methods; the
// Control Plane wiring (cmd/cwv2) adapts these to queries/commands. No Control Plane types leak in here.
type Service struct {
	dir       string
	providers *providerStore
	usage     *usageStore
}

// New constructs the service rooted at <projectDir>/aiworkspace (created if missing).
func New(projectDir string) *Service {
	d := filepath.Join(projectDir, "aiworkspace")
	_ = os.MkdirAll(d, 0o755)
	return &Service{dir: d, providers: newProviderStore(d), usage: newUsageStore(d)}
}

// --- Providers ---------------------------------------------------------------------------------------

func (s *Service) ProvidersPublic() []map[string]any { return publicView(s.providers.load()) }
func (s *Service) Kinds() []map[string]any           { return KindsPublic() }

func (s *Service) AddProvider(kind, name, baseURL string) (Provider, error) {
	return s.providers.add(kind, name, baseURL)
}
func (s *Service) UpdateProvider(id, name, baseURL string, priority int) ([]map[string]any, error) {
	ps, err := s.providers.update(id, name, baseURL, priority, false)
	return publicView(ps), err
}
func (s *Service) RemoveProvider(id string) []map[string]any {
	return publicView(s.providers.remove(id))
}
func (s *Service) SetDefault(id string) []map[string]any {
	return publicView(s.providers.setDefault(id))
}
func (s *Service) SetEnabled(id string, on bool) []map[string]any {
	return publicView(s.providers.setEnabled(id, on))
}

func (s *Service) AddAccount(providerID, label, org, key, model string) ([]map[string]any, error) {
	ps, err := s.providers.addAccount(providerID, label, org, key, model)
	return publicView(ps), err
}
func (s *Service) UpdateAccount(providerID, accountID, label, org, key, model string) ([]map[string]any, error) {
	ps, err := s.providers.updateAccount(providerID, accountID, label, org, key, model)
	return publicView(ps), err
}
func (s *Service) RemoveAccount(providerID, accountID string) ([]map[string]any, error) {
	ps, err := s.providers.removeAccount(providerID, accountID)
	return publicView(ps), err
}

// TestProvider runs a FREE model-list check against the provider using its first account key (never a
// paid inference call) and records the result.
func (s *Service) TestProvider(ctx context.Context, id string) (TestResult, error) {
	p, ok := s.providers.get(id)
	if !ok {
		return TestResult{}, os.ErrNotExist
	}
	key := ""
	if len(p.Accounts) > 0 {
		key = s.providers.resolveKey(p.Accounts[0].SecretRef)
	}
	res := testConnection(ctx, p.Kind, p.BaseURL, key)
	s.providers.recordTest(id, res.OK, res.Message, res.Models)
	return res, nil
}

// --- Usage -------------------------------------------------------------------------------------------

func (s *Service) RecordUsage(e UsageEvent)   { s.usage.record(e) }
func (s *Service) UsageSummary() UsageSummary { return s.usage.summary() }

// --- Dashboard ---------------------------------------------------------------------------------------

// Dashboard is the single summary the Dashboard page reads. Fields not yet produced (compression/cache
// ratios, proxy, companion) report honest zero/absent states until their phases land.
func (s *Service) Dashboard() map[string]any {
	ps := s.providers.load()
	enabled := 0
	var def *Provider
	for i := range ps {
		if ps[i].Enabled {
			enabled++
		}
		if ps[i].IsDefault && ps[i].Enabled && def == nil {
			def = &ps[i]
		}
	}
	if def == nil {
		for i := range ps {
			if ps[i].Enabled {
				def = &ps[i]
				break
			}
		}
	}
	providerName, model := "—", "—"
	local := false
	if def != nil {
		providerName = def.Name
		local = kindDesc(def.Kind).Local
		if len(def.Accounts) > 0 && def.Accounts[0].DefaultModel != "" {
			model = def.Accounts[0].DefaultModel
		} else if kd := kindDesc(def.Kind); len(kd.Models) > 0 {
			model = kd.Models[0]
		}
	}
	sum := s.usage.summary()
	health := "ok"
	if len(ps) == 0 {
		health = "setup" // no providers configured yet
	}
	return map[string]any{
		"provider":         providerName,
		"providerLocal":    local,
		"model":            model,
		"workspace":        "—", // Workspaces arrive in a later phase
		"proxy":            map[string]any{"running": false},
		"companion":        map[string]any{"present": false},
		"health":           health,
		"providersCount":   len(ps),
		"enabledCount":     enabled,
		"usage":            sum,
		"compressionRatio": 0, // Optimizers phase
		"cacheHitRatio":    0, // Cache phase
		"generatedAt":      time.Now().UTC().Format(time.RFC3339),
	}
}
