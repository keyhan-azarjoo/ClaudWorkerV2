package aiworkspace

import (
	"context"
	"strings"
	"testing"
)

func run(t *testing.T, id, content string) OptimizeOutput {
	t.Helper()
	o, ok := GetOptimizer(id)
	if !ok {
		t.Fatalf("optimizer %q not registered", id)
	}
	out, err := o.Optimize(context.Background(), OptimizeInput{Content: []byte(content), Config: nil})
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return out
}

func TestPhase5Registered(t *testing.T) {
	for _, id := range []string{"json", "yaml", "xml", "log", "stacktrace", "terminal", "largefile", "generated", "binary"} {
		if _, ok := GetOptimizer(id); !ok {
			t.Fatalf("optimizer %q not registered", id)
		}
	}
}

func TestJSONMinify(t *testing.T) {
	out := run(t, "json", `{ "a": 1,  "b": [1, 2,   3] }`)
	got := strings.TrimSpace(string(out.Content))
	if got != `{"a":1,"b":[1,2,3]}` {
		t.Fatalf("json not minified: %q", got)
	}
	if out.TokensAfter > out.TokensBefore {
		t.Fatal("json minify grew tokens")
	}
}

func TestJSONInvalidFallback(t *testing.T) {
	out := run(t, "json", "not json   \n\n\n")
	if strings.Contains(string(out.Content), "   ") {
		t.Fatal("fallback should trim trailing whitespace")
	}
}

func TestYAMLStripsComments(t *testing.T) {
	out := run(t, "yaml", "# header comment\nkey: value  # inline stays\n\n\n\nother: 1\n")
	s := string(out.Content)
	if strings.Contains(s, "header comment") {
		t.Fatalf("full-line comment not stripped: %q", s)
	}
	if !strings.Contains(s, "# inline stays") {
		t.Fatal("inline comment must be preserved (may be inside a value)")
	}
}

func TestXMLStripsCommentsAndWhitespace(t *testing.T) {
	out := run(t, "xml", "<a>  <b>x</b>   </a><!-- gone -->")
	s := string(out.Content)
	if strings.Contains(s, "gone") {
		t.Fatalf("comment not removed: %q", s)
	}
	if strings.Contains(s, "> ") {
		t.Fatalf("inter-tag whitespace not collapsed: %q", s)
	}
}

func TestLogCollapsesRepeats(t *testing.T) {
	out := run(t, "log", "boom\nboom\nboom\nok\n")
	s := string(out.Content)
	if !strings.Contains(s, "(×3)") {
		t.Fatalf("repeats not collapsed: %q", s)
	}
	if out.TokensAfter > out.TokensBefore {
		t.Fatal("log optimizer grew tokens")
	}
}

func TestStacktraceKeepsTopFrames(t *testing.T) {
	var b strings.Builder
	b.WriteString("Error: boom\n")
	for i := 0; i < 12; i++ {
		b.WriteString("    at frame" + string(rune('a'+i)) + " (file.js:1)\n")
	}
	o, _ := GetOptimizer("stacktrace")
	out, _ := o.Optimize(context.Background(), OptimizeInput{Content: []byte(b.String()), Config: map[string]any{"keepFrames": 3, "dropVendor": false}})
	s := string(out.Content)
	if !strings.Contains(s, "omitted") {
		t.Fatalf("expected an omitted-frames marker: %q", s)
	}
	if strings.Count(s, "at frame") != 3 {
		t.Fatalf("expected 3 kept frames, got %d", strings.Count(s, "at frame"))
	}
}

func TestTerminalStripsAnsiAndCR(t *testing.T) {
	out := run(t, "terminal", "loading...\rdone\x1b[0m   \n")
	s := strings.TrimSpace(string(out.Content))
	if s != "done" {
		t.Fatalf("expected 'done', got %q", s)
	}
}

func TestLargeFileTruncates(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line"
	}
	o, _ := GetOptimizer("largefile")
	out, _ := o.Optimize(context.Background(), OptimizeInput{Content: []byte(strings.Join(lines, "\n")), Config: map[string]any{"headLines": 40, "tailLines": 20}})
	got := strings.Count(string(out.Content), "\n") + 1
	if got != 61 { // 40 head + 1 marker + 20 tail
		t.Fatalf("expected 61 lines, got %d", got)
	}
	if !strings.Contains(string(out.Content), "omitted") {
		t.Fatal("expected elision marker")
	}
}

func TestGeneratedFilter(t *testing.T) {
	out := run(t, "generated", "src/main.go\npackage-lock.json\napp.min.js\nsrc/util.go\ngo.sum\n")
	s := string(out.Content)
	for _, gone := range []string{"package-lock.json", "app.min.js", "go.sum"} {
		if strings.Contains(s, gone) {
			t.Fatalf("%s should be filtered: %q", gone, s)
		}
	}
	if !strings.Contains(s, "src/main.go") || !strings.Contains(s, "src/util.go") {
		t.Fatalf("real sources must be kept: %q", s)
	}
}

func TestBinaryFilter(t *testing.T) {
	out := run(t, "binary", "abc\x00\x01\x02\x03binary\x00stuff")
	if !strings.Contains(string(out.Content), "binary content omitted") {
		t.Fatalf("binary not replaced: %q", out.Content)
	}
	// Text passes through.
	out2 := run(t, "binary", "just normal text")
	if string(out2.Content) != "just normal text" {
		t.Fatalf("text should pass through: %q", out2.Content)
	}
}
