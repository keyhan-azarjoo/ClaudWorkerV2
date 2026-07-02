package verify

import (
	"context"
	"fmt"
	"strings"
)

// VisualDriver is the human-like interaction surface a real user would perform. Concrete drivers wrap
// Appium/adb/WebDriver/simulators (future plugins); the VisualVerifier is driver-agnostic so those
// arrive without changing the Verification Engine. Headless verification is used ONLY when visual
// interaction is impossible (a driver returns ErrVisualUnavailable → the verifier reports Blocked so
// the caller can fall back to a non-visual verifier).
type VisualDriver interface {
	Launch(target string) error
	Navigate(to string) error
	Click(selector string) error
	Type(selector, text string) error
	Scroll(direction string) error
	PairDevice(id string) error
	Screenshot(name string) (Evidence, error)
	OCR() (string, error)
	State() (map[string]string, error)
}

// ErrVisualUnavailable signals that visual interaction is impossible in this environment (no display,
// no device). The verifier maps it to Blocked, inviting a headless fallback elsewhere.
var ErrVisualUnavailable = fmt.Errorf("visual interaction unavailable")

// VisualVerifier drives a real user's journey — launch, navigate, click, type, scroll, pair, capture
// screenshots, OCR, compare against expectations, verify state, and report differences.
type VisualVerifier struct {
	VName  string
	VType  Type // usually TypeVisual; may be TypeUI/TypeDevice with the same driver
	VCaps  []string
	Driver VisualDriver
}

func (v VisualVerifier) Name() string           { return v.VName }
func (v VisualVerifier) Type() Type             { return v.VType }
func (v VisualVerifier) Capabilities() []string { return v.VCaps }

// Verify performs the human-like journey then checks the expectations, attaching a screenshot as
// evidence. Outcomes: Blocked if the environment makes visual interaction impossible; Inconclusive on
// any other driver error mid-journey; Fail if expectations do not hold (with the differences in
// Detail); Pass when every expectation holds.
func (v VisualVerifier) Verify(ctx context.Context, req Request) (Result, error) {
	res := Result{Metrics: map[string]float64{}}
	if v.Driver == nil {
		res.Outcome = Blocked
		res.Summary = "no visual driver available"
		return res, nil
	}

	step := func(desc string, err error) (bool, Result) {
		if err == nil {
			res.Logs = append(res.Logs, desc)
			return true, res
		}
		if err == ErrVisualUnavailable {
			res.Outcome = Blocked
			res.Summary = "visual interaction impossible: " + desc + " (use a headless verifier)"
		} else {
			res.Outcome = Inconclusive
			res.Summary = "interaction error during: " + desc
			res.Detail = err.Error()
		}
		return false, res
	}

	if ok, r := step("launch "+req.Target, v.Driver.Launch(req.Target)); !ok {
		return r, nil
	}
	for _, s := range req.Steps {
		if ok, r := step(describeStep(s), v.runStep(s)); !ok {
			return r, nil
		}
	}

	// Capture evidence like a tester would.
	if ev, err := v.Driver.Screenshot("final"); err == nil {
		res.Evidence = append(res.Evidence, ev)
	}
	ocr, _ := v.Driver.OCR()
	state, _ := v.Driver.State()

	// Compare against expectations, collecting every difference.
	var diffs []string
	for _, exp := range req.Expectations {
		if ok, why := checkExpectation(exp, ocr, state); !ok {
			diffs = append(diffs, why)
		}
	}
	res.Metrics["expectations"] = float64(len(req.Expectations))
	res.Metrics["differences"] = float64(len(diffs))
	if len(diffs) > 0 {
		res.Outcome = Fail
		res.Summary = fmt.Sprintf("%d of %d expectations failed", len(diffs), len(req.Expectations))
		res.Detail = strings.Join(diffs, "\n")
		return res, nil
	}
	res.Outcome = Pass
	res.Summary = fmt.Sprintf("all %d expectations verified", len(req.Expectations))
	return res, nil
}

func (v VisualVerifier) runStep(s Step) error {
	switch s.Action {
	case "navigate":
		return v.Driver.Navigate(s.Value)
	case "click":
		return v.Driver.Click(s.Selector)
	case "type":
		return v.Driver.Type(s.Selector, s.Value)
	case "scroll":
		return v.Driver.Scroll(s.Value)
	case "pair":
		return v.Driver.PairDevice(s.Value)
	default:
		return fmt.Errorf("unknown step action %q", s.Action)
	}
}

func describeStep(s Step) string {
	if s.Selector != "" {
		return fmt.Sprintf("%s %q=%q", s.Action, s.Selector, s.Value)
	}
	return fmt.Sprintf("%s %q", s.Action, s.Value)
}

// checkExpectation evaluates one expectation against the captured OCR text / UI state.
func checkExpectation(exp Expectation, ocr string, state map[string]string) (bool, string) {
	switch exp.Kind {
	case "text_present":
		if strings.Contains(ocr, exp.Value) {
			return true, ""
		}
		return false, fmt.Sprintf("expected text %q not found on screen", exp.Value)
	case "state":
		if state[exp.Key] == exp.Value {
			return true, ""
		}
		return false, fmt.Sprintf("state %q = %q, expected %q", exp.Key, state[exp.Key], exp.Value)
	default:
		return false, fmt.Sprintf("unknown expectation kind %q", exp.Kind)
	}
}
