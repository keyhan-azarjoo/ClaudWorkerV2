package verify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPVerifier is a real verifier plugin for API and Website verification: it GETs (or POSTs) a URL
// and checks the status code and optional body substrings. Deterministic; no tokens. The HTTP client
// is injectable for tests.
type HTTPVerifier struct {
	VName       string
	VType       Type     // TypeAPI or TypeUI (web)
	VCaps       []string // e.g. ["http","api"] or ["http","web"]
	URL         string   // target (may be overridden by Request.Target)
	Method      string   // default GET
	WantStatus  int      // expected status (default 200)
	MustContain []string // body substrings that must be present
	Client      *http.Client
}

func (v HTTPVerifier) Name() string           { return v.VName }
func (v HTTPVerifier) Type() Type             { return v.VType }
func (v HTTPVerifier) Capabilities() []string { return v.VCaps }

// Verify performs the request and maps the response to an outcome.
func (v HTTPVerifier) Verify(ctx context.Context, req Request) (Result, error) {
	res := Result{Metrics: map[string]float64{}}
	url := v.URL
	if req.Target != "" {
		url = req.Target
	}
	if url == "" {
		res.Outcome = Blocked
		res.Summary = "no URL to verify"
		return res, nil
	}
	method := v.Method
	if method == "" {
		method = http.MethodGet
	}
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	want := v.WantStatus
	if want == 0 {
		want = 200
	}

	r, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		res.Outcome = Blocked
		res.Summary = "bad request: " + err.Error()
		return res, nil
	}
	resp, err := client.Do(r)
	if err != nil {
		// Could not reach the target → cannot determine (not a Fail).
		res.Outcome = Inconclusive
		res.Summary = "request failed: " + err.Error()
		return res, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	res.Metrics["status"] = float64(resp.StatusCode)
	res.Metrics["bytes"] = float64(len(body))
	res.Evidence = append(res.Evidence, Evidence{Kind: "http", Ref: url, Note: fmt.Sprintf("%s %d", method, resp.StatusCode)})

	var diffs []string
	if resp.StatusCode != want {
		diffs = append(diffs, fmt.Sprintf("status %d, expected %d", resp.StatusCode, want))
	}
	for _, sub := range v.MustContain {
		if !strings.Contains(string(body), sub) {
			diffs = append(diffs, fmt.Sprintf("body missing %q", sub))
		}
	}
	if len(diffs) > 0 {
		res.Outcome = Fail
		res.Summary = fmt.Sprintf("%s check failed", v.VType)
		res.Detail = strings.Join(diffs, "; ")
		return res, nil
	}
	res.Outcome = Pass
	res.Summary = fmt.Sprintf("%s %s → %d", method, url, resp.StatusCode)
	return res, nil
}
