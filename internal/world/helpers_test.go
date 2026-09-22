package world

import (
	"slices"
	"testing"
)

// codesOf extracts the sorted, deduplicated codes from a finding list. Tests
// assert on codes rather than message text so wording stays free to improve —
// the same convention internal/schema already follows.
func codesOf(fs []Finding) []Code {
	var out []Code
	for _, f := range fs {
		if !slices.Contains(out, f.Code) {
			out = append(out, f.Code)
		}
	}
	slices.Sort(out)
	return out
}

// wantCodes asserts that fs carries exactly the given codes.
func wantCodes(t *testing.T, fs []Finding, want ...Code) {
	t.Helper()
	slices.Sort(want)
	got := codesOf(fs)
	if !slices.Equal(got, want) {
		t.Errorf("codes = %v, want %v\nfindings: %v", got, want, fs)
	}
}

// noFindings asserts a clean parse or load.
func noFindings(t *testing.T, fs []Finding) {
	t.Helper()
	if len(fs) != 0 {
		t.Fatalf("expected no findings, got %d:\n%v", len(fs), Findings(fs))
	}
}

// file builds a lore file from a frontmatter block and a body, with LF
// endings. Cases exercising CRLF or a BOM build their bytes by hand.
func file(frontmatter, body string) []byte {
	return []byte("---\n" + frontmatter + "---\n" + body)
}
