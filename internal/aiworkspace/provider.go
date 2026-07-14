// Package aiworkspace is the AI Workspace subsystem: a local-first place to manage AI providers,
// track token usage, and (in later phases) run optimizers, caches and context packs. It is a pure
// service package — it holds no Control Plane logic; the wiring layer (cmd/cwv2) exposes it over the
// Control Plane. Persistence is per-project JSON stores under the project's engine home, matching the
// repoStore/ruleStore convention (single-dep discipline: stdlib only).
package aiworkspace

// ProviderKind is a supported provider family. New kinds are added to Kinds without touching callers.
type ProviderKind = string

// Account is one credential set for a provider (a provider may hold several — different keys/orgs).
// The API KEY itself is NEVER stored here: only a keychain reference (SecretRef) and a non-sensitive
// last-4 hint for masked display. This is the standing "credentials must never be exposed" rule.
type Account struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Org          string   `json:"org,omitempty"`
	SecretRef    string   `json:"secretRef,omitempty"` // keychain service name; the VALUE never lives in JSON
	KeyHint      string   `json:"keyHint,omitempty"`   // last 4 chars, for "••••1234" display (not sensitive)
	DefaultModel string   `json:"defaultModel,omitempty"`
	Models       []string `json:"models,omitempty"`
	RateLimit    int      `json:"rateLimit,omitempty"` // requests/min, informational
}

// Provider is one configured AI provider with its accounts and priority.
type Provider struct {
	ID          string       `json:"id"`
	Kind        ProviderKind `json:"kind"`
	Name        string       `json:"name"`
	BaseURL     string       `json:"baseURL,omitempty"`
	Priority    int          `json:"priority"`
	IsDefault   bool         `json:"isDefault"`
	Enabled     bool         `json:"enabled"`
	Accounts    []Account    `json:"accounts"`
	LastTestAt  string       `json:"lastTestAt,omitempty"`
	LastTestOK  bool         `json:"lastTestOK"`
	LastTestMsg string       `json:"lastTestMsg,omitempty"`
}

// KindDesc describes a built-in provider family for the Add form and for connection testing. Local=true
// marks a free/local provider (Ollama) — the default, honoring the "never spend money" rule.
type KindDesc struct {
	Kind           ProviderKind `json:"kind"`
	Label          string       `json:"label"`
	DefaultBaseURL string       `json:"defaultBaseURL"`
	Models         []string     `json:"models"`
	Local          bool         `json:"local"`
	// modelsPath is the FREE model-list endpoint used by "Test connection" — no completion/inference
	// call is ever made, so testing never spends tokens/money. Empty means "cannot test remotely".
	modelsPath string
	// auth selects how the key is presented on the models-list request.
	auth authStyle
}

type authStyle int

const (
	authBearer    authStyle = iota // Authorization: Bearer <key>  (OpenAI-compatible)
	authAnthropic                  // x-api-key + anthropic-version
	authGoogleQS                   // ?key=<key> query string
	authNone                       // local, no key (Ollama)
)

// Kinds is the built-in provider catalog. Ollama (local, free) is listed first and is the recommended
// default. All "Test connection" paths are FREE list endpoints (no inference), never a paid call.
var Kinds = []KindDesc{
	{Kind: "ollama", Label: "Ollama (local)", DefaultBaseURL: "http://localhost:11434", Models: []string{"llama3.1", "qwen2.5", "mistral", "phi3"}, Local: true, modelsPath: "/api/tags", auth: authNone},
	{Kind: "anthropic", Label: "Anthropic", DefaultBaseURL: "https://api.anthropic.com", Models: []string{"claude-opus-4-8", "claude-sonnet-5", "claude-haiku-4-5-20251001"}, modelsPath: "/v1/models", auth: authAnthropic},
	{Kind: "openai", Label: "OpenAI", DefaultBaseURL: "https://api.openai.com", Models: []string{"gpt-4o", "gpt-4o-mini", "o3-mini"}, modelsPath: "/v1/models", auth: authBearer},
	{Kind: "google", Label: "Google Gemini", DefaultBaseURL: "https://generativelanguage.googleapis.com", Models: []string{"gemini-1.5-pro", "gemini-1.5-flash"}, modelsPath: "/v1beta/models", auth: authGoogleQS},
	{Kind: "grok", Label: "xAI Grok", DefaultBaseURL: "https://api.x.ai", Models: []string{"grok-2", "grok-2-mini"}, modelsPath: "/v1/models", auth: authBearer},
	{Kind: "openrouter", Label: "OpenRouter", DefaultBaseURL: "https://openrouter.ai/api", Models: []string{"auto"}, modelsPath: "/v1/models", auth: authBearer},
	{Kind: "deepseek", Label: "DeepSeek", DefaultBaseURL: "https://api.deepseek.com", Models: []string{"deepseek-chat", "deepseek-reasoner"}, modelsPath: "/v1/models", auth: authBearer},
	{Kind: "mistral", Label: "Mistral", DefaultBaseURL: "https://api.mistral.ai", Models: []string{"mistral-large-latest", "mistral-small-latest"}, modelsPath: "/v1/models", auth: authBearer},
	{Kind: "azure", Label: "Azure OpenAI", DefaultBaseURL: "", Models: []string{}, modelsPath: "/openai/models?api-version=2024-06-01", auth: authBearer},
	{Kind: "custom", Label: "Custom endpoint", DefaultBaseURL: "", Models: []string{}, modelsPath: "/v1/models", auth: authBearer},
}

// kindDesc returns the descriptor for a kind (custom fallback if unknown).
func kindDesc(kind ProviderKind) KindDesc {
	for _, k := range Kinds {
		if k.Kind == kind {
			return k
		}
	}
	return Kinds[len(Kinds)-1] // custom
}

// KindsPublic returns the catalog for the UI Add form (drops internal fields).
func KindsPublic() []map[string]any {
	out := make([]map[string]any, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, map[string]any{
			"kind": k.Kind, "label": k.Label, "defaultBaseURL": k.DefaultBaseURL,
			"models": k.Models, "local": k.Local, "canTest": k.modelsPath != "",
		})
	}
	return out
}
