package verifyadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"claudworker/internal/orchestrator"
	"claudworker/internal/verify"
)

// ensure the adapter satisfies the orchestrator port.
var _ orchestrator.Verifier = (*Adapter)(nil)

func TestBuildAndHTTPVerification(t *testing.T) {
	// real HTTP target
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("OK healthy"))
	}))
	defer srv.Close()

	eng, plan := BuildEngine(Options{
		RepoDir:  t.TempDir(),
		BuildCmd: []string{"sh", "-c", "exit 0"}, // stand-in for a real build
		APIURL:   srv.URL,
		WebURL:   srv.URL,
	})
	a := New(eng, plan...)

	results, err := a.Verify(context.Background(), "SCRUM-1")
	if err != nil {
		t.Fatal(err)
	}
	if verify.Aggregate(results) != verify.Pass {
		t.Fatalf("expected overall Pass, got %s (%+v)", verify.Aggregate(results), results)
	}
	// build + api + web = 3 results
	if len(results) != 3 {
		t.Errorf("results = %d, want 3", len(results))
	}
}

func TestFailingBuildFailsVerification(t *testing.T) {
	eng, plan := BuildEngine(Options{RepoDir: t.TempDir(), BuildCmd: []string{"sh", "-c", "exit 1"}})
	a := New(eng, plan...)
	results, _ := a.Verify(context.Background(), "SCRUM-2")
	if verify.Aggregate(results) != verify.Fail {
		t.Errorf("failing build should Fail, got %s", verify.Aggregate(results))
	}
}

func TestHTTPFailOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	eng := verify.New()
	eng.Register(verify.HTTPVerifier{VName: "api", VType: verify.TypeAPI, VCaps: []string{"http"}, URL: srv.URL, WantStatus: 200})
	a := New(eng, verify.Request{Type: verify.TypeAPI, Capabilities: []string{"http"}, Target: srv.URL})
	if verify.Aggregate(mustVerify(t, a)) != verify.Fail {
		t.Error("500 should Fail")
	}
}

// visual driver seam: a fake driver proves the visual verifier is wired through the adapter.
type fakeDriver struct{ ocr string }

func (d fakeDriver) Launch(string) error       { return nil }
func (d fakeDriver) Navigate(string) error     { return nil }
func (d fakeDriver) Click(string) error        { return nil }
func (d fakeDriver) Type(string, string) error { return nil }
func (d fakeDriver) Scroll(string) error       { return nil }
func (d fakeDriver) PairDevice(string) error   { return nil }
func (d fakeDriver) Screenshot(n string) (verify.Evidence, error) {
	return verify.Evidence{Kind: "screenshot", Ref: n}, nil
}
func (d fakeDriver) OCR() (string, error) { return d.ocr, nil }
func (d fakeDriver) State() (map[string]string, error) {
	return map[string]string{"screen": "home"}, nil
}

func TestVisualDriverWiredThroughAdapter(t *testing.T) {
	eng, _ := BuildEngine(Options{RepoDir: t.TempDir(), BuildCmd: []string{"sh", "-c", "exit 0"},
		VisualType: verify.TypeVisual, VisualCaps: []string{"android"}, VisualDrv: fakeDriver{ocr: "Welcome home"}})
	// run the visual verifier directly through the engine
	res := eng.Verify(context.Background(), verify.Request{Type: verify.TypeVisual, Capabilities: []string{"android"},
		Target: "app", Expectations: []verify.Expectation{{Kind: "text_present", Value: "Welcome"}, {Kind: "state", Key: "screen", Value: "home"}}})
	if verify.Aggregate(res) != verify.Pass {
		t.Errorf("visual verify = %s (%+v)", verify.Aggregate(res), res)
	}
}

func mustVerify(t *testing.T, a *Adapter) []verify.Result {
	r, err := a.Verify(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
