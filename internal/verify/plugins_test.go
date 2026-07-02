package verify

import (
	"context"
	"testing"
	"time"
)

func TestCommandVerifierPassFail(t *testing.T) {
	pass := CommandVerifier{VName: "unit", VType: TypeUnit, VCaps: []string{"sh"}, Command: []string{"sh", "-c", "exit 0"}}
	r, _ := pass.Verify(context.Background(), Request{})
	if r.Outcome != Pass || r.Metrics["exit_code"] != 0 {
		t.Errorf("pass case = %+v", r)
	}
	fail := CommandVerifier{VName: "unit", VType: TypeUnit, Command: []string{"sh", "-c", "echo boom >&2; exit 3"}}
	r, _ = fail.Verify(context.Background(), Request{})
	if r.Outcome != Fail || r.Metrics["exit_code"] != 3 {
		t.Errorf("fail case = %+v", r)
	}
	if len(r.Logs) == 0 {
		t.Error("expected command output captured in logs")
	}
}

func TestCommandVerifierTimeoutInconclusive(t *testing.T) {
	v := CommandVerifier{VName: "build", VType: TypeBuild, Command: []string{"sh", "-c", "sleep 5"}}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	r, _ := v.Verify(ctx, Request{})
	if r.Outcome != Inconclusive {
		t.Errorf("timeout outcome = %s, want inconclusive", r.Outcome)
	}
}

func TestCommandVerifierThroughEngine(t *testing.T) {
	e := New(CommandVerifier{VName: "unit", VType: TypeUnit, VCaps: []string{"sh"}, Command: []string{"sh", "-c", "exit 0"}})
	got := e.Verify(context.Background(), Request{Type: TypeUnit, Capabilities: []string{"sh"}})
	if len(got) != 1 || got[0].Outcome != Pass || got[0].Verifier != "unit" {
		t.Errorf("engine+command = %+v", got)
	}
}

// fakeDriver is a scripted human-like driver: records the journey, serves canned OCR/state, and can
// fail a chosen action (including ErrVisualUnavailable) to exercise Blocked/Inconclusive paths.
type fakeDriver struct {
	ocr      string
	state    map[string]string
	failOn   string
	failErr  error
	journey  []string
	launched bool
}

func (d *fakeDriver) rec(s string) { d.journey = append(d.journey, s) }
func (d *fakeDriver) maybeFail(action string) error {
	if action == d.failOn {
		if d.failErr != nil {
			return d.failErr
		}
		return context.Canceled
	}
	return nil
}
func (d *fakeDriver) Launch(t string) error {
	d.launched = true
	d.rec("launch:" + t)
	return d.maybeFail("launch")
}
func (d *fakeDriver) Navigate(to string) error   { d.rec("nav:" + to); return d.maybeFail("navigate") }
func (d *fakeDriver) Click(sel string) error     { d.rec("click:" + sel); return d.maybeFail("click") }
func (d *fakeDriver) Type(sel, tx string) error  { d.rec("type:" + sel); return d.maybeFail("type") }
func (d *fakeDriver) Scroll(dir string) error    { d.rec("scroll:" + dir); return d.maybeFail("scroll") }
func (d *fakeDriver) PairDevice(id string) error { d.rec("pair:" + id); return d.maybeFail("pair") }
func (d *fakeDriver) Screenshot(n string) (Evidence, error) {
	return Evidence{Kind: "screenshot", Ref: "/tmp/" + n + ".png"}, nil
}
func (d *fakeDriver) OCR() (string, error)              { return d.ocr, nil }
func (d *fakeDriver) State() (map[string]string, error) { return d.state, nil }

func TestVisualVerifierPassJourney(t *testing.T) {
	d := &fakeDriver{ocr: "Welcome to MyOTGO\nDevice paired", state: map[string]string{"screen": "home"}}
	v := VisualVerifier{VName: "android-visual", VType: TypeVisual, VCaps: []string{"android", "ocr"}, Driver: d}
	req := Request{
		Target: "com.myotgo.app",
		Steps: []Step{
			{Action: "navigate", Value: "devices"},
			{Action: "click", Selector: "add_device"},
			{Action: "pair", Value: "esp32-1"},
		},
		Expectations: []Expectation{
			{Kind: "text_present", Value: "Device paired"},
			{Kind: "state", Key: "screen", Value: "home"},
		},
	}
	r, _ := v.Verify(context.Background(), req)
	if r.Outcome != Pass {
		t.Fatalf("journey = %+v", r)
	}
	if !d.launched || len(d.journey) != 4 { // launch + 3 steps
		t.Errorf("journey not driven like a user: %v", d.journey)
	}
	if len(r.Evidence) == 0 || r.Metrics["differences"] != 0 {
		t.Errorf("evidence/metrics = %+v", r)
	}
}

func TestVisualVerifierReportsDifferences(t *testing.T) {
	d := &fakeDriver{ocr: "Error 500", state: map[string]string{"screen": "error"}}
	v := VisualVerifier{VName: "android-visual", VType: TypeVisual, Driver: d}
	r, _ := v.Verify(context.Background(), Request{
		Target: "app",
		Expectations: []Expectation{
			{Kind: "text_present", Value: "Device paired"},
			{Kind: "state", Key: "screen", Value: "home"},
		},
	})
	if r.Outcome != Fail || r.Metrics["differences"] != 2 {
		t.Fatalf("expected 2 differences Fail, got %+v", r)
	}
	if r.Detail == "" {
		t.Error("differences must be reported in Detail")
	}
}

func TestVisualVerifierBlockedWhenVisualImpossible(t *testing.T) {
	d := &fakeDriver{failOn: "launch", failErr: ErrVisualUnavailable}
	v := VisualVerifier{VName: "android-visual", VType: TypeVisual, Driver: d}
	r, _ := v.Verify(context.Background(), Request{Target: "app"})
	if r.Outcome != Blocked {
		t.Errorf("visual-impossible outcome = %s, want blocked (headless fallback)", r.Outcome)
	}
}

func TestVisualVerifierInconclusiveOnInteractionError(t *testing.T) {
	d := &fakeDriver{failOn: "click"}
	v := VisualVerifier{VName: "android-visual", VType: TypeVisual, Driver: d}
	r, _ := v.Verify(context.Background(), Request{Target: "app", Steps: []Step{{Action: "click", Selector: "x"}}})
	if r.Outcome != Inconclusive {
		t.Errorf("interaction error outcome = %s, want inconclusive", r.Outcome)
	}
}
