// Package config loads and validates the ClaudWorker V2 project configuration.
//
// Config is the ONLY project-specific surface (docs/13_Config.md, Law 16). Nothing here is
// MyOTGO-specific. Thresholds are configurable defaults — never hardcoded in engine source
// (owner decision 2, docs/13_Config.md). Secrets are referenced by NAME only, never by value
// (NFR-6); resolution happens elsewhere (internal/secrets).
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the parsed cwv2.yaml for one project.
type Config struct {
	Project    string            `yaml:"project"`
	EngineHome string            `yaml:"engine_home"`
	Jira       Jira              `yaml:"jira"`
	GitHub     GitHub            `yaml:"github"`
	Repos      []Repo            `yaml:"repos"`
	Component  map[string]string `yaml:"component_map"`
	Workflow   Workflow          `yaml:"workflow"`
	Defaults   Defaults          `yaml:"defaults"`
	UsageGuard UsageGuard        `yaml:"usage_guard"`
	QA         QA                `yaml:"qa"`
	Dashboard  Dashboard         `yaml:"dashboard"`

	// path is the file this config was loaded from (not serialized).
	path string `yaml:"-"`
}

type Jira struct {
	BaseURL   string                `yaml:"base_url"`
	Auth      SecretPair            `yaml:"auth"`
	WorkJQL   string                `yaml:"work_jql"`
	StatusMap map[string]StringList `yaml:"status_map"`
	ACField   string                `yaml:"ac_field"`
	Labels    map[string]string     `yaml:"labels"`
}

// StringList accepts either a single scalar or a YAML sequence, always yielding []string.
// The status_map in docs/13_Config.md mixes both forms (e.g. ready: [..], done: "Done").
type StringList []string

func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*s = StringList{value.Value}
		return nil
	}
	var arr []string
	if err := value.Decode(&arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

// SecretPair holds the NAMES of vault entries, never the secret values (NFR-6).
type SecretPair struct {
	UserSecret  string `yaml:"user_secret"`
	TokenSecret string `yaml:"token_secret"`
}

type GitHub struct {
	User           string         `yaml:"user"`
	CommitIdentity CommitIdentity `yaml:"commit_identity"`
}

type CommitIdentity struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type Repo struct {
	Name      string   `yaml:"name"`
	URL       string   `yaml:"url"`
	DevBranch string   `yaml:"dev_branch"`
	Plugin    string   `yaml:"plugin"`
	PathHints []string `yaml:"path_hints"`
}

type Workflow struct {
	MaxConcurrent      int   `yaml:"max_concurrent"`
	Merge              Merge `yaml:"merge"`
	RefreshBeforeGates bool  `yaml:"refresh_before_gates"`
}

type Merge struct {
	Strategy     string `yaml:"strategy"`
	DeleteBranch bool   `yaml:"delete_branch"`
}

// Defaults holds every tunable threshold. All are overridable per project without recompiling
// (owner decision 2). Zero values are replaced by ApplyDefaults with the engine built-in fallbacks.
type Defaults struct {
	AbandonedDays       int         `yaml:"abandoned_days"`
	LargeIssueThreshold int         `yaml:"large_issue_threshold"`
	SplitThreshold      int         `yaml:"split_threshold"`
	ImgdiffThreshold    float64     `yaml:"imgdiff_threshold"`
	LockTTL             LockTTL     `yaml:"lock_ttl"`
	RetryLimits         RetryLimits `yaml:"retry_limits"`
}

// LockTTL is the per-scope lease TTL in seconds. V1 has exactly three scopes (docs/15_LockManager.md).
type LockTTL struct {
	Issue  int `yaml:"issue"`
	Device int `yaml:"device"`
	Merge  int `yaml:"merge"`
}

type RetryLimits struct {
	MaxAttempts  int `yaml:"max_attempts"`
	FlakyRetries int `yaml:"flaky_retries"`
	MergeRefresh int `yaml:"merge_refresh"`
}

type UsageGuard struct {
	Provider  string `yaml:"provider"`
	PausePct  int    `yaml:"pause_pct"`
	ResumePct int    `yaml:"resume_pct"`
	FailOpen  bool   `yaml:"fail_open"`
}

type QA struct {
	PreferRealDevice bool    `yaml:"prefer_real_device"`
	ImgdiffTolerance float64 `yaml:"imgdiff_tolerance"`
}

type Dashboard struct {
	Bind  string `yaml:"bind"`
	Port  int    `yaml:"port"`
	Token string `yaml:"token"`
}

// builtinDefaults are the engine's fallback thresholds (the lowest precedence layer,
// docs/13_Config.md "Precedence"). A project config overrides any of these.
func builtinDefaults() Defaults {
	return Defaults{
		AbandonedDays:       30,
		LargeIssueThreshold: 8,
		SplitThreshold:      5,
		ImgdiffThreshold:    0.02,
		LockTTL:             LockTTL{Issue: 3600, Device: 1800, Merge: 300},
		RetryLimits:         RetryLimits{MaxAttempts: 3, FlakyRetries: 2, MergeRefresh: 3},
	}
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Load reads, parses, applies defaults, and validates a cwv2.yaml at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	c, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	c.path = path
	return c, nil
}

// Parse decodes YAML bytes, applies defaults, then validates. Exposed for testing without a file.
func Parse(raw []byte) (*Config, error) {
	var c Config
	// yaml.Unmarshal ignores unknown keys by default (forward-compat with future config additions).
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ApplyDefaults fills zero-valued thresholds with the engine built-in fallbacks (config precedence).
func (c *Config) ApplyDefaults() {
	d := builtinDefaults()
	if c.Defaults.AbandonedDays == 0 {
		c.Defaults.AbandonedDays = d.AbandonedDays
	}
	if c.Defaults.LargeIssueThreshold == 0 {
		c.Defaults.LargeIssueThreshold = d.LargeIssueThreshold
	}
	if c.Defaults.SplitThreshold == 0 {
		c.Defaults.SplitThreshold = d.SplitThreshold
	}
	if c.Defaults.ImgdiffThreshold == 0 {
		c.Defaults.ImgdiffThreshold = d.ImgdiffThreshold
	}
	if c.Defaults.LockTTL.Issue == 0 {
		c.Defaults.LockTTL.Issue = d.LockTTL.Issue
	}
	if c.Defaults.LockTTL.Device == 0 {
		c.Defaults.LockTTL.Device = d.LockTTL.Device
	}
	if c.Defaults.LockTTL.Merge == 0 {
		c.Defaults.LockTTL.Merge = d.LockTTL.Merge
	}
	if c.Defaults.RetryLimits.MaxAttempts == 0 {
		c.Defaults.RetryLimits.MaxAttempts = d.RetryLimits.MaxAttempts
	}
	if c.Defaults.RetryLimits.FlakyRetries == 0 {
		c.Defaults.RetryLimits.FlakyRetries = d.RetryLimits.FlakyRetries
	}
	if c.Defaults.RetryLimits.MergeRefresh == 0 {
		c.Defaults.RetryLimits.MergeRefresh = d.RetryLimits.MergeRefresh
	}
	if c.Workflow.MaxConcurrent == 0 {
		c.Workflow.MaxConcurrent = 3
	}
	if c.Dashboard.Port == 0 {
		c.Dashboard.Port = 8790
	}
	if c.Dashboard.Bind == "" {
		c.Dashboard.Bind = "127.0.0.1"
	}
}

// Validate refuses a config the engine cannot safely run, with a precise error (docs/14_Deployment.md:
// "refuses to run on any failure with a precise message — no silent wrong defaults").
func (c *Config) Validate() error {
	if c.Project == "" {
		return fmt.Errorf("project: required (a project name)")
	}
	if c.EngineHome == "" {
		return fmt.Errorf("engine_home: required (path to the engine home, e.g. /Volumes/Extreme SSD/cwv2-home)")
	}
	if c.Jira.BaseURL == "" {
		return fmt.Errorf("jira.base_url: required")
	}
	if c.Jira.WorkJQL == "" {
		return fmt.Errorf("jira.work_jql: required (the work queue JQL)")
	}
	// Commit identity is owner-mandated (C-2): author must be keyhanazarjoo.
	if c.GitHub.CommitIdentity.Name == "" || c.GitHub.CommitIdentity.Email == "" {
		return fmt.Errorf("github.commit_identity: name and email are required (author identity, C-2)")
	}
	if len(c.Repos) == 0 {
		return fmt.Errorf("repos: at least one repo is required")
	}
	for i, r := range c.Repos {
		if r.Name == "" {
			return fmt.Errorf("repos[%d].name: required", i)
		}
		if r.URL == "" {
			return fmt.Errorf("repos[%d].url: required (repo %q)", i, r.Name)
		}
		if r.DevBranch == "" {
			return fmt.Errorf("repos[%d].dev_branch: required (repo %q)", i, r.Name)
		}
		if r.Plugin == "" {
			return fmt.Errorf("repos[%d].plugin: required (repo %q)", i, r.Name)
		}
	}
	if c.UsageGuard.PausePct != 0 && c.UsageGuard.ResumePct != 0 &&
		c.UsageGuard.ResumePct > c.UsageGuard.PausePct {
		return fmt.Errorf("usage_guard: resume_pct (%d) must be <= pause_pct (%d)",
			c.UsageGuard.ResumePct, c.UsageGuard.PausePct)
	}
	return nil
}

// SecretNames returns every vault-entry name referenced by the config, so doctor can check
// resolvability without ever reading a value.
func (c *Config) SecretNames() []string {
	var names []string
	add := func(n string) {
		if n != "" {
			names = append(names, n)
		}
	}
	add(c.Jira.Auth.UserSecret)
	add(c.Jira.Auth.TokenSecret)
	return names
}
