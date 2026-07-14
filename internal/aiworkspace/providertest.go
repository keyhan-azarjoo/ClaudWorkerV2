package aiworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TestResult reports a connection test. Money rule: a test ONLY hits the provider's FREE model-list
// endpoint — it never runs a completion/inference, so it never spends tokens.
type TestResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Models  []string `json:"models,omitempty"`
}

// testConnection lists models from a provider using the account key, with no inference call. Returns a
// clear message on failure so the UI can show why.
func testConnection(ctx context.Context, kind, baseURL, key string) TestResult {
	kd := kindDesc(kind)
	if kd.modelsPath == "" {
		return TestResult{OK: false, Message: "this provider cannot be tested remotely"}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = kd.DefaultBaseURL
	}
	if strings.TrimSpace(baseURL) == "" {
		return TestResult{OK: false, Message: "a base URL is required for this provider"}
	}
	if kd.auth != authNone && strings.TrimSpace(key) == "" {
		return TestResult{OK: false, Message: "add an API key first"}
	}

	url := strings.TrimRight(baseURL, "/") + kd.modelsPath
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	switch kd.auth {
	case authBearer:
		req.Header.Set("Authorization", "Bearer "+key)
	case authAnthropic:
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case authGoogleQS:
		q := req.URL.Query()
		q.Set("key", key)
		req.URL.RawQuery = q.Encode()
	case authNone:
		// local, no auth
	}

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return TestResult{OK: false, Message: connErr(kind, err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TestResult{OK: false, Message: fmt.Sprintf("HTTP %d from provider", resp.StatusCode)}
	}
	models := parseModels(kind, body)
	msg := "connected"
	if len(models) > 0 {
		msg = fmt.Sprintf("connected — %d models available", len(models))
	}
	return TestResult{OK: true, Message: msg, Models: models}
}

func connErr(kind string, err error) string {
	if kind == "ollama" {
		return "could not reach Ollama — is it running? (" + err.Error() + ")"
	}
	return err.Error()
}

// parseModels extracts model ids from the various provider list shapes (best-effort, tolerant).
func parseModels(kind string, body []byte) []string {
	// Ollama: {"models":[{"name":"llama3.1"},...]}
	if kind == "ollama" {
		var o struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(body, &o) == nil {
			var out []string
			for _, m := range o.Models {
				if m.Name != "" {
					out = append(out, m.Name)
				}
			}
			return out
		}
		return nil
	}
	// Google: {"models":[{"name":"models/gemini-1.5-pro"},...]}
	if kind == "google" {
		var g struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(body, &g) == nil {
			var out []string
			for _, m := range g.Models {
				out = append(out, strings.TrimPrefix(m.Name, "models/"))
			}
			return out
		}
		return nil
	}
	// OpenAI-compatible: {"data":[{"id":"gpt-4o"},...]}
	var oa struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &oa) == nil && len(oa.Data) > 0 {
		var out []string
		for _, m := range oa.Data {
			if m.ID != "" {
				out = append(out, m.ID)
			}
		}
		return out
	}
	return nil
}
