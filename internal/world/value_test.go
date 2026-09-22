package world

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVisibilityUnmarshal(t *testing.T) {
	tests := []struct {
		in       string
		wantKind VisibilityKind
		wantAct  string
		wantOK   bool
	}{
		{"public", VisibilityPublic, "", true},
		{"internal", VisibilityInternal, "", true},
		{"spoiler:act2", VisibilitySpoiler, "act2", true},
		{"spoiler:the_long_road", VisibilitySpoiler, "the_long_road", true},

		// Malformed values parse into an invalid Visibility rather than
		// failing here: every enum fault is reported by the validator, in one
		// place, with a file and a line.
		{"spoiler:", "", "", false},
		{"spoiler", "", "", false},
		{"Public", "", "", false},
		{"secret", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var holder struct {
				V Visibility `yaml:"v"`
			}
			if err := yaml.Unmarshal([]byte("v: "+quote(tt.in)), &holder); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			v := holder.V
			if v.Kind != tt.wantKind || v.Act != tt.wantAct {
				t.Errorf("got {%q %q}, want {%q %q}", v.Kind, v.Act, tt.wantKind, tt.wantAct)
			}
			if v.Valid() != tt.wantOK {
				t.Errorf("Valid() = %v, want %v", v.Valid(), tt.wantOK)
			}
			// Raw always survives, so an error message can quote exactly what
			// was authored.
			if v.Raw != tt.in {
				t.Errorf("Raw = %q, want %q", v.Raw, tt.in)
			}
		})
	}
}

func TestVisibilityRoundTrip(t *testing.T) {
	for _, in := range []string{"public", "internal", "spoiler:act2"} {
		var holder struct {
			V Visibility `yaml:"v"`
		}
		if err := yaml.Unmarshal([]byte("v: "+in), &holder); err != nil {
			t.Fatalf("unmarshal %q: %v", in, err)
		}
		out, err := yaml.Marshal(holder)
		if err != nil {
			t.Fatalf("marshal %q: %v", in, err)
		}
		if got := string(out); got != "v: "+in+"\n" {
			t.Errorf("round trip of %q produced %q", in, got)
		}
	}
}

// TestTruthAcceptsBooleans guards the one place where YAML's own types get in
// the way: `truth: true` is a !!bool node, and forcing authors to quote it
// would be a papercut on the most-read field of a statement.
func TestTruthAcceptsBooleans(t *testing.T) {
	tests := []struct {
		in   string
		want Truth
		ok   bool
	}{
		{"true", TruthTrue, true},
		{"false", TruthFalse, true},
		{`"true"`, TruthTrue, true},
		{"unresolved", TruthUnresolved, true},
		{"maybe", "maybe", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var holder struct {
				T Truth `yaml:"t"`
			}
			if err := yaml.Unmarshal([]byte("t: "+tt.in), &holder); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if holder.T != tt.want {
				t.Errorf("got %q, want %q", holder.T, tt.want)
			}
			if holder.T.Valid() != tt.ok {
				t.Errorf("Valid() = %v, want %v", holder.T.Valid(), tt.ok)
			}
		})
	}
}

func TestEnumValidity(t *testing.T) {
	for _, s := range []Status{StatusDraft, StatusCanon, StatusDeprecated, StatusNonCanon} {
		if !s.Valid() {
			t.Errorf("status %q should be valid", s)
		}
	}
	for _, s := range []Status{"", "Canon", "published"} {
		if s.Valid() {
			t.Errorf("status %q should be invalid", s)
		}
	}

	for _, c := range []Confidence{ConfidenceHigh, ConfidenceMedium, ConfidenceLow, ""} {
		if !c.Valid() {
			t.Errorf("confidence %q should be valid; empty means unstated", c)
		}
	}
	if Confidence("certain").Valid() {
		t.Error(`confidence "certain" should be invalid`)
	}

	for _, p := range []Precision{PrecisionExact, PrecisionApproximate, ""} {
		if !p.Valid() {
			t.Errorf("precision %q should be valid; empty means unstated", p)
		}
	}
	if Precision("circa").Valid() {
		t.Error(`precision "circa" should be invalid`)
	}
}

// TestIntervalZero covers the distinction the validator leans on: an Interval
// that says nothing cannot be compared, and must not be guessed at.
func TestIntervalZero(t *testing.T) {
	var empty Interval
	if !empty.Empty() {
		t.Error("a zero Interval should report Empty")
	}
	if (Interval{Era: "third_reign"}).Empty() {
		t.Error("an Interval with only an era is not empty")
	}
	if (Interval{Earliest: ptr(380)}).Empty() {
		t.Error("an Interval with only a year is not empty")
	}
}

func quote(s string) string { return `"` + s + `"` }

func ptr[T any](v T) *T { return &v }
