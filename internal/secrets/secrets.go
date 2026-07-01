// Package secrets resolves secret VALUES from their configured NAMES at runtime.
//
// Secrets are never stored in config or the repo (NFR-6, C-2/C-5). Resolution order
// (docs/13_Config.md): macOS keychain, then Azure Key Vault, then environment. Values are returned
// only to the caller that needs them and are never logged.
package secrets

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// ErrNotFound is returned when a secret name cannot be resolved by any provider.
var ErrNotFound = errors.New("secret not found in any provider")

// Resolver resolves secret names to values. The default resolver chains keychain, Azure KV, env.
type Resolver struct {
	// probes let tests stub external commands; nil means use the real ones.
	keychain func(name string) (string, bool)
	azure    func(name string) (string, bool)
	env      func(name string) (string, bool)
}

// NewResolver returns the default resolver using the real system providers.
func NewResolver() *Resolver {
	return &Resolver{
		keychain: keychainLookup,
		azure:    azureLookup,
		env:      envLookup,
	}
}

// Resolve returns the value for a secret name, trying each provider in order.
func (r *Resolver) Resolve(name string) (string, error) {
	for _, p := range []func(string) (string, bool){r.keychain, r.azure, r.env} {
		if p == nil {
			continue
		}
		if v, ok := p(name); ok && v != "" {
			return v, nil
		}
	}
	return "", ErrNotFound
}

// CanResolve reports whether a name resolves, WITHOUT returning the value (used by doctor so no
// secret value is ever surfaced/logged).
func (r *Resolver) CanResolve(name string) bool {
	_, err := r.Resolve(name)
	return err == nil
}

// keychainLookup reads a generic password from the macOS keychain by service name.
func keychainLookup(name string) (string, bool) {
	out, err := exec.Command("security", "find-generic-password", "-s", name, "-w").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

// azureLookup reads a secret from Azure Key Vault via the `az` CLI, if configured.
// The vault is taken from CWV2_AZURE_KEYVAULT; absent that, this provider is a no-op.
func azureLookup(name string) (string, bool) {
	vault := os.Getenv("CWV2_AZURE_KEYVAULT")
	if vault == "" {
		return "", false
	}
	out, err := exec.Command("az", "keyvault", "secret", "show",
		"--vault-name", vault, "--name", name, "--query", "value", "-o", "tsv").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

// envLookup reads a secret from the environment. The name is upper-cased with non-alphanumerics
// turned into underscores (e.g. "jira_token" -> "JIRA_TOKEN").
func envLookup(name string) (string, bool) {
	key := envKey(name)
	v, ok := os.LookupEnv(key)
	return v, ok
}

func envKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
