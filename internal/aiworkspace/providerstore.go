package aiworkspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// providerStore is the per-project persistent registry of AI providers (providers.json). API key VALUES
// are stored in the OS keychain (via `security`), never in this file — only a keychain reference and a
// last-4 hint. Same load/save/mutex shape as repoStore.
type providerStore struct {
	path string
	mu   sync.Mutex
}

func newProviderStore(dir string) *providerStore {
	return &providerStore{path: filepath.Join(dir, "providers.json")}
}

func (s *providerStore) load() []Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var out []Provider
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func (s *providerStore) save(ps []Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, err := json.MarshalIndent(ps, "", "  "); err == nil {
		_ = os.WriteFile(s.path, b, 0o600) // 0600: even though no secrets live here, keep it private
	}
}

// publicView returns providers safe to send to the client: the keychain SecretRef is stripped and a
// hasKey flag added, so the raw reference never leaves the backend. KeyHint (last-4) is kept for display.
func publicView(ps []Provider) []map[string]any {
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		accts := make([]map[string]any, 0, len(p.Accounts))
		for _, a := range p.Accounts {
			accts = append(accts, map[string]any{
				"id": a.ID, "label": a.Label, "org": a.Org,
				"keyHint": a.KeyHint, "hasKey": a.SecretRef != "",
				"defaultModel": a.DefaultModel, "models": a.Models, "rateLimit": a.RateLimit,
			})
		}
		out = append(out, map[string]any{
			"id": p.ID, "kind": p.Kind, "name": p.Name, "baseURL": p.BaseURL,
			"priority": p.Priority, "isDefault": p.IsDefault, "enabled": p.Enabled,
			"accounts": accts, "lastTestAt": p.LastTestAt, "lastTestOK": p.LastTestOK, "lastTestMsg": p.LastTestMsg,
		})
	}
	return out
}

// slug makes a stable, filesystem/keychain-safe id fragment.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "x"
	}
	return out
}

// nextID returns a short unique id derived from a base + nanosecond suffix (no external uuid dep).
func nextID(base string) string {
	return fmt.Sprintf("%s-%d", slug(base), time.Now().UnixNano()%1_000_000)
}

// add creates a provider. Name defaults to the kind label; BaseURL defaults to the kind's default.
func (s *providerStore) add(kind, name, baseURL string) (Provider, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return Provider{}, fmt.Errorf("provider kind is required")
	}
	kd := kindDesc(kind)
	name = strings.TrimSpace(name)
	if name == "" {
		name = kd.Label
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = kd.DefaultBaseURL
	}
	ps := s.load()
	p := Provider{ID: nextID(kind), Kind: kind, Name: name, BaseURL: baseURL, Priority: len(ps), Enabled: true}
	if len(ps) == 0 {
		p.IsDefault = true // first provider added becomes the default
	}
	ps = append(ps, p)
	s.save(ps)
	return p, nil
}

func (s *providerStore) find(ps []Provider, id string) int {
	for i := range ps {
		if ps[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *providerStore) update(id, name, baseURL string, priority int, rateLimitSet bool) ([]Provider, error) {
	ps := s.load()
	i := s.find(ps, id)
	if i < 0 {
		return ps, fmt.Errorf("unknown provider %q", id)
	}
	if strings.TrimSpace(name) != "" {
		ps[i].Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(baseURL) != "" {
		ps[i].BaseURL = strings.TrimSpace(baseURL)
	}
	if priority >= 0 {
		ps[i].Priority = priority
	}
	s.save(ps)
	return ps, nil
}

// remove deletes a provider and PURGES every account key it held from the keychain.
func (s *providerStore) remove(id string) []Provider {
	ps := s.load()
	var out []Provider
	for _, p := range ps {
		if p.ID == id {
			for _, a := range p.Accounts {
				if a.SecretRef != "" {
					keychainDelete(a.SecretRef)
				}
			}
			continue
		}
		out = append(out, p)
	}
	// If we removed the default, promote the first remaining enabled provider.
	if !anyDefault(out) {
		for i := range out {
			if out[i].Enabled {
				out[i].IsDefault = true
				break
			}
		}
	}
	s.save(out)
	return out
}

func anyDefault(ps []Provider) bool {
	for _, p := range ps {
		if p.IsDefault {
			return true
		}
	}
	return false
}

func (s *providerStore) setDefault(id string) []Provider {
	ps := s.load()
	for i := range ps {
		ps[i].IsDefault = ps[i].ID == id
	}
	s.save(ps)
	return ps
}

func (s *providerStore) setEnabled(id string, enabled bool) []Provider {
	ps := s.load()
	i := s.find(ps, id)
	if i >= 0 {
		ps[i].Enabled = enabled
		if !enabled && ps[i].IsDefault { // disabling the default → move default to another enabled one
			ps[i].IsDefault = false
			for j := range ps {
				if ps[j].Enabled {
					ps[j].IsDefault = true
					break
				}
			}
		}
	}
	s.save(ps)
	return ps
}

// addAccount stores the key in the keychain and records only its reference + last-4 hint.
func (s *providerStore) addAccount(providerID, label, org, key, defaultModel string) ([]Provider, error) {
	key = strings.TrimSpace(key)
	ps := s.load()
	i := s.find(ps, providerID)
	if i < 0 {
		return ps, fmt.Errorf("unknown provider %q", providerID)
	}
	if strings.TrimSpace(label) == "" {
		label = fmt.Sprintf("account %d", len(ps[i].Accounts)+1)
	}
	acct := Account{ID: nextID("acct"), Label: strings.TrimSpace(label), Org: strings.TrimSpace(org), DefaultModel: strings.TrimSpace(defaultModel)}
	kd := kindDesc(ps[i].Kind)
	if key != "" || kd.auth != authNone {
		ref := "cw-aiw-" + ps[i].ID + "-" + acct.ID
		if key != "" {
			if err := keychainStore(ref, key); err != nil {
				return ps, fmt.Errorf("could not store key in keychain: %w", err)
			}
			acct.SecretRef = ref
			acct.KeyHint = last4(key)
		}
	}
	ps[i].Accounts = append(ps[i].Accounts, acct)
	s.save(ps)
	return ps, nil
}

// updateAccount changes label/org/model and, if a new key is supplied, rotates it in the keychain.
func (s *providerStore) updateAccount(providerID, accountID, label, org, key, defaultModel string) ([]Provider, error) {
	ps := s.load()
	i := s.find(ps, providerID)
	if i < 0 {
		return ps, fmt.Errorf("unknown provider %q", providerID)
	}
	for j := range ps[i].Accounts {
		if ps[i].Accounts[j].ID != accountID {
			continue
		}
		a := &ps[i].Accounts[j]
		if strings.TrimSpace(label) != "" {
			a.Label = strings.TrimSpace(label)
		}
		a.Org = strings.TrimSpace(org)
		if strings.TrimSpace(defaultModel) != "" {
			a.DefaultModel = strings.TrimSpace(defaultModel)
		}
		if key = strings.TrimSpace(key); key != "" {
			ref := a.SecretRef
			if ref == "" {
				ref = "cw-aiw-" + ps[i].ID + "-" + a.ID
			}
			if err := keychainStore(ref, key); err != nil {
				return ps, fmt.Errorf("could not store key in keychain: %w", err)
			}
			a.SecretRef = ref
			a.KeyHint = last4(key)
		}
		s.save(ps)
		return ps, nil
	}
	return ps, fmt.Errorf("unknown account %q", accountID)
}

func (s *providerStore) removeAccount(providerID, accountID string) ([]Provider, error) {
	ps := s.load()
	i := s.find(ps, providerID)
	if i < 0 {
		return ps, fmt.Errorf("unknown provider %q", providerID)
	}
	var kept []Account
	for _, a := range ps[i].Accounts {
		if a.ID == accountID {
			if a.SecretRef != "" {
				keychainDelete(a.SecretRef)
			}
			continue
		}
		kept = append(kept, a)
	}
	ps[i].Accounts = kept
	s.save(ps)
	return ps, nil
}

// resolveKey reads an account's key from the keychain. Used ONLY server-side (connection test); the
// value is never returned to the client.
func (s *providerStore) resolveKey(secretRef string) string {
	if secretRef == "" {
		return ""
	}
	out, err := exec.Command("security", "find-generic-password", "-s", secretRef, "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

func (s *providerStore) get(id string) (Provider, bool) {
	ps := s.load()
	i := s.find(ps, id)
	if i < 0 {
		return Provider{}, false
	}
	return ps[i], true
}

func (s *providerStore) recordTest(id string, ok bool, msg string, models []string) {
	ps := s.load()
	i := s.find(ps, id)
	if i < 0 {
		return
	}
	ps[i].LastTestAt = time.Now().UTC().Format(time.RFC3339)
	ps[i].LastTestOK = ok
	ps[i].LastTestMsg = msg
	if ok && len(models) > 0 && len(ps[i].Accounts) > 0 {
		ps[i].Accounts[0].Models = models
	}
	s.save(ps)
}

func last4(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

// keychainStore writes/updates a generic password in the macOS keychain. The value is passed on argv,
// which is briefly visible to a local process listing — acceptable for a single-user local tool, and no
// worse than the existing readers. On non-macOS `security` is absent and this returns an error.
func keychainStore(service, value string) error {
	cmd := exec.Command("security", "add-generic-password", "-U", "-s", service, "-a", "claudworker", "-w", value)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain write failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func keychainDelete(service string) {
	_ = exec.Command("security", "delete-generic-password", "-s", service).Run()
}
