package verify

import (
	"context"
	"testing"
	"time"
)

// funcVerifier is a tiny plugin used to test the Engine's selection/aggregation without side effects.
type funcVerifier struct {
	name string
	typ  Type
	caps []string
	fn   func() Result
}

func (f funcVerifier) Name() string                                    { return f.name }
func (f funcVerifier) Type() Type                                      { return f.typ }
func (f funcVerifier) Capabilities() []string                          { return f.caps }
func (f funcVerifier) Verify(context.Context, Request) (Result, error) { return f.fn(), nil }

func steady() func() time.Time {
	t := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	return func() time.Time { cur := t; t = t.Add(time.Millisecond); return cur }
}

func TestCapabilitySelection(t *testing.T) {
	e := New(
		funcVerifier{name: "android-visual", typ: TypeVisual, caps: []string{"android", "ocr"}, fn: func() Result { return Result{Outcome: Pass} }},
		funcVerifier{name: "ios-visual", typ: TypeVisual, caps: []string{"ios", "ocr"}, fn: func() Result { return Result{Outcome: Pass} }},
		funcVerifier{name: "unit", typ: TypeUnit, caps: []string{"go"}, fn: func() Result { return Result{Outcome: Pass} }},
	)
	// type + capability filter → only android-visual
	sel := e.Select(Request{Type: TypeVisual, Capabilities: []string{"android"}})
	if len(sel) != 1 || sel[0].Name() != "android-visual" {
		t.Fatalf("selection = %v, want [android-visual]", names(sel))
	}
	// type only → both visual verifiers (sorted)
	sel = e.Select(Request{Type: TypeVisual})
	if len(sel) != 2 || sel[0].Name() != "android-visual" || sel[1].Name() != "ios-visual" {
		t.Errorf("selection = %v", names(sel))
	}
	// unmatched capability → none
	if len(e.Select(Request{Type: TypeVisual, Capabilities: []string{"windows"}})) != 0 {
		t.Error("expected no match for unsupported capability")
	}
}

func TestVerifyNoCapableVerifierIsBlocked(t *testing.T) {
	e := New()
	got := e.Verify(context.Background(), Request{Type: TypePCB})
	if len(got) != 1 || got[0].Outcome != Blocked {
		t.Fatalf("want single Blocked result, got %+v", got)
	}
}

func TestVerifySetsMetadataAndDuration(t *testing.T) {
	e := WithOptions([]Option{WithClock(steady())},
		funcVerifier{name: "u", typ: TypeUnit, caps: nil, fn: func() Result { return Result{Outcome: Pass, Summary: "ok"} }},
	)
	got := e.Verify(context.Background(), Request{Type: TypeUnit})
	if len(got) != 1 {
		t.Fatal("want one result")
	}
	if got[0].Verifier != "u" || got[0].Type != TypeUnit || got[0].Duration != time.Millisecond {
		t.Errorf("metadata/duration = %+v", got[0])
	}
}

func TestAggregatePrecedence(t *testing.T) {
	cases := []struct {
		outs []Outcome
		want Outcome
	}{
		{[]Outcome{Pass, Pass}, Pass},
		{[]Outcome{Pass, Deferred}, Deferred},
		{[]Outcome{Deferred, Inconclusive}, Inconclusive},
		{[]Outcome{Inconclusive, Blocked}, Blocked},
		{[]Outcome{Blocked, Fail, Pass}, Fail},
		{nil, Inconclusive},
	}
	for _, c := range cases {
		var rs []Result
		for _, o := range c.outs {
			rs = append(rs, Result{Outcome: o})
		}
		if got := Aggregate(rs); got != c.want {
			t.Errorf("Aggregate(%v) = %s, want %s", c.outs, got, c.want)
		}
	}
}

// TestFuturePluginNoCoreChange proves a new verification kind is added purely by registering a
// plugin — the core Engine is untouched.
func TestFuturePluginNoCoreChange(t *testing.T) {
	e := New()
	e.Register(funcVerifier{name: "smell-test", typ: Type("smell"), caps: []string{"nose"},
		fn: func() Result { return Result{Outcome: Pass, Summary: "smells fine"} }})
	got := e.Verify(context.Background(), Request{Type: Type("smell"), Capabilities: []string{"nose"}})
	if len(got) != 1 || got[0].Outcome != Pass || got[0].Verifier != "smell-test" {
		t.Errorf("future plugin not dispatched: %+v", got)
	}
}

func names(vs []Verifier) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name()
	}
	return out
}
