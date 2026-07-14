package aiworkspace

import (
	"context"
	"strings"
	"testing"
)

// TestBuiltinsRegistered ensures all Phase-2 optimizers self-register.
func TestBuiltinsRegistered(t *testing.T) {
	want := []string{"compress", "markdown", "gitdiff", "tree", "dedup"}
	for _, id := range want {
		if _, ok := GetOptimizer(id); !ok {
			t.Fatalf("optimizer %q not registered", id)
		}
	}
	if len(ListOptimizers()) < len(want) {
		t.Fatalf("expected >= %d optimizers", len(want))
	}
}

// TestOptimizersNeverGrowTokens — an optimizer must not increase tokens on representative input.
func TestOptimizersNeverGrowTokens(t *testing.T) {
	samples := map[string]string{
		"markdown": "# Title\n\n\n\n<!-- a comment -->\nsome text   \n\n\n",
		"compress": "line one    \n\n\n\n\nline two\t\t\n",
		"dedup":    "same line\nsame line\nsame line\nunique\n",
		"gitdiff":  "diff --git a/f b/f\nindex 111..222 100644\n--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n ctx1\n-old\n+new\n ctx2\n",
		"tree":     "src/a.go\nsrc/b.go\nnode_modules/x/index.js\nnode_modules/y/index.js\n",
	}
	for id, in := range samples {
		o, _ := GetOptimizer(id)
		out, err := o.Optimize(context.Background(), OptimizeInput{Kind: o.Meta().Kinds[0], Content: []byte(in), Config: nil})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if out.TokensAfter > out.TokensBefore {
			t.Fatalf("%s increased tokens: %d -> %d", id, out.TokensBefore, out.TokensAfter)
		}
	}
}

func TestMarkdownStripsComments(t *testing.T) {
	o, _ := GetOptimizer("markdown")
	out, _ := o.Optimize(context.Background(), OptimizeInput{Content: []byte("hi <!-- secret --> there")})
	if strings.Contains(string(out.Content), "secret") {
		t.Fatalf("HTML comment not stripped: %q", out.Content)
	}
}

func TestDedupRemovesDuplicates(t *testing.T) {
	o, _ := GetOptimizer("dedup")
	out, _ := o.Optimize(context.Background(), OptimizeInput{Content: []byte("a\nb\na\nb\nc\n")})
	got := strings.Count(string(out.Content), "a")
	if got != 1 {
		t.Fatalf("expected 1 'a' after dedup, got %d in %q", got, out.Content)
	}
}

func TestGitdiffDropsContext(t *testing.T) {
	o, _ := GetOptimizer("gitdiff")
	in := "@@ -1,3 +1,3 @@\n ctx1\n-old\n+new\n ctx2\n"
	out, _ := o.Optimize(context.Background(), OptimizeInput{Content: []byte(in)})
	if strings.Contains(string(out.Content), "ctx1") {
		t.Fatalf("context line not dropped at contextLines=0: %q", out.Content)
	}
	if !strings.Contains(string(out.Content), "+new") || !strings.Contains(string(out.Content), "-old") {
		t.Fatalf("changes must be kept: %q", out.Content)
	}
}

// TestRunOptimizerRecordsAndBanks — running through the service updates stats and banks saved tokens as
// a usage event (so the Dashboard's saved/compression figures are real).
func TestRunOptimizerRecordsAndBanks(t *testing.T) {
	svc := New(t.TempDir())
	content := "# Doc\n\n\n\n<!-- big long comment that adds tokens here -->\ntext line\n\n\n\n"
	res, err := svc.RunOptimizer(context.Background(), "markdown", "markdown", content, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res["saved"].(int) <= 0 {
		t.Fatalf("expected saved > 0, got %v", res["saved"])
	}
	// Stats persisted.
	found := false
	for _, o := range svc.OptimizersList() {
		m := o["meta"].(OptimizerMeta)
		if m.ID == "markdown" {
			st := o["stats"].(OptimizerStats)
			if st.Runs != 1 || st.SavedTokens <= 0 {
				t.Fatalf("stats not recorded: %+v", st)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("markdown optimizer missing from list")
	}
	// Usage banked.
	if svc.UsageSummary().TodaySaved <= 0 {
		t.Fatal("saved tokens not banked into usage")
	}
	// Dashboard compression ratio now reflects the run.
	if svc.Dashboard()["compressionRatio"].(int) <= 0 {
		t.Fatal("dashboard compression ratio should be > 0 after a run")
	}
}

// TestOptimizerEnableToggle — disabling persists and reflects in the list.
func TestOptimizerEnableToggle(t *testing.T) {
	svc := New(t.TempDir())
	svc.SetOptimizerEnabled("compress", false)
	for _, o := range svc.OptimizersList() {
		if o["meta"].(OptimizerMeta).ID == "compress" && o["enabled"] != false {
			t.Fatal("compress should be disabled")
		}
	}
}
