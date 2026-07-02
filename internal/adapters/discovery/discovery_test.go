package discovery

import (
	"context"
	"testing"

	"github.com/myotgo/ClaudWorkerV2/internal/resource"
)

func fakeGet(body string) HTTPGetter {
	return func(context.Context, string) ([]byte, error) { return []byte(body), nil }
}
func fakeRun(out string) CmdRunner {
	return func(context.Context, string, ...string) ([]byte, error) { return []byte(out), nil }
}

func TestAccountsDiscovery(t *testing.T) {
	a := Accounts{Kind: resource.KindClaudeAccount, Dirs: map[string]string{"MyOTGO": "/exists", "Gone": "/nope"},
		Stat: func(p string) bool { return p == "/exists" }}
	rs, _ := a.Discover()
	byName := map[string]resource.Resource{}
	for _, r := range rs {
		byName[r.Name] = r
	}
	if byName["MyOTGO"].Health != resource.HealthHealthy || byName["Gone"].Health != resource.HealthDown {
		t.Errorf("account health = %+v", byName)
	}
	if byName["MyOTGO"].Kind != resource.KindClaudeAccount || byName["MyOTGO"].Labels["engine"] != "claude" {
		t.Errorf("account mapping = %+v", byName["MyOTGO"])
	}
}

func TestOllamaAndOpenAICompat(t *testing.T) {
	ol := Provider{ID: "ollama", BaseURL: "http://x:11434", Ollama: true, Get: fakeGet(`{"models":[{"name":"llama3.2"},{"name":"qwen"}]}`)}
	rs, _ := ol.Discover()
	if len(rs) != 1 || rs[0].Kind != resource.KindLocalRuntime || rs[0].Labels["models"] != "llama3.2,qwen" {
		t.Errorf("ollama = %+v", rs)
	}
	lm := Provider{ID: "lmstudio", BaseURL: "http://x:1234", Get: fakeGet(`{"data":[{"id":"local-model"}]}`)}
	rs, _ = lm.Discover()
	if len(rs) != 1 || rs[0].Labels["models"] != "local-model" {
		t.Errorf("lmstudio = %+v", rs)
	}
	// unreachable → nothing
	down := Provider{ID: "vllm", BaseURL: "http://x", Get: func(context.Context, string) ([]byte, error) { return nil, context.DeadlineExceeded }}
	if rs, _ := down.Discover(); len(rs) != 0 {
		t.Errorf("unreachable provider should yield nothing: %+v", rs)
	}
}

func TestAdbDiscovery(t *testing.T) {
	a := Adb{Run: fakeRun("List of devices attached\nR58M123\tdevice\nemulator-5554\toffline\n")}
	rs, _ := a.Discover()
	if len(rs) != 1 || rs[0].Kind != resource.KindAndroidDevice || rs[0].Labels["serial"] != "R58M123" {
		t.Errorf("adb = %+v", rs)
	}
}

func TestSimctlDiscovery(t *testing.T) {
	s := Simctl{Run: fakeRun("== Devices ==\n-- iOS 18.6 --\n    iPhone 15 (ABC-123) (Booted)\n    iPad (DEF) (Shutdown)\n")}
	rs, _ := s.Discover()
	if len(rs) != 1 || rs[0].Kind != resource.KindIPhone || rs[0].Labels["simulator"] != "true" {
		t.Errorf("simctl = %+v", rs)
	}
}

func TestSerialDiscovery(t *testing.T) {
	s := Serial{Glob: func(pat string) ([]string, error) {
		if pat == "/dev/cu.usbserial*" {
			return []string{"/dev/cu.usbserial-1110"}, nil
		}
		return nil, nil
	}}
	rs, _ := s.Discover()
	if len(rs) != 1 || rs[0].Kind != resource.KindESP32 {
		t.Errorf("serial = %+v", rs)
	}
}

func TestCompositeMergesAndDedupesAndFeedsManager(t *testing.T) {
	comp := Composite{
		Accounts{Kind: resource.KindClaudeAccount, Dirs: map[string]string{"A": ""}},
		Adb{Run: fakeRun("R1\tdevice\n")},
		Static{Resources: []resource.Resource{{ID: "acct-a", Kind: resource.KindClaudeAccount, Name: "dup"}}}, // dup ID
		Static{Resources: []resource.Resource{{ID: "host-macmini", Kind: resource.KindMacMini, Name: "Mac Mini"}}},
	}
	rs, _ := comp.Discover()
	ids := map[string]bool{}
	for _, r := range rs {
		if ids[r.ID] {
			t.Errorf("duplicate id %q not deduped", r.ID)
		}
		ids[r.ID] = true
	}
	if !ids["acct-a"] || !ids["host-macmini"] {
		t.Errorf("composite missing resources: %v", ids)
	}
	// feeds the real Resource Manager (reuse; no dup logic)
	m := resource.New()
	if err := m.Discover(comp); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot()) != len(rs) {
		t.Errorf("manager did not ingest all discovered resources")
	}
}
