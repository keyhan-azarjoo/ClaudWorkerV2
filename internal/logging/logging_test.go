package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONFormatAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "warn", "json")
	log.Info("hidden") // below warn -> filtered
	log.Warn("shown", "k", "v")
	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Errorf("info line should be filtered at warn level: %q", out)
	}
	if !strings.Contains(out, `"msg":"shown"`) || !strings.Contains(out, `"k":"v"`) {
		t.Errorf("expected JSON warn line, got %q", out)
	}
}

func TestTextFormatDefault(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "text")
	log.Info("hello", "n", 1)
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected text output, got %q", buf.String())
	}
}
