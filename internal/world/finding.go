package world

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Code identifies a class of fault in a world file. Callers and tests match on
// the code rather than on message text, so wording stays free to improve —
// the same contract internal/schema makes.
//
// The codes here are the ones a parser can reach: framing and YAML. Rule codes
// are declared by internal/validate, as values of this same type, so that
// everything a build reports sorts and prints one way.
type Code string

const (
	CodeUnreadable              Code = "unreadable"
	CodeNoFrontmatter           Code = "no_frontmatter"
	CodeUnterminatedFrontmatter Code = "unterminated_frontmatter"
	CodeEmptyFrontmatter        Code = "empty_frontmatter"
	CodeParse                   Code = "parse"
	CodeUnknownField            Code = "unknown_field"
)

// Severity separates what stops a build from what a build mentions.
//
// The split is load-bearing rather than cosmetic. Warnings are continuity
// observations — a character at an event they were dead for — and a check that
// is right most of the time must never block a merge, because a gate that is
// wrong often enough trains the team to bypass it.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one fault, located as precisely as the input allows: a file, a
// line in that file, and the frontmatter path that produced it.
type Finding struct {
	Code     Code
	Severity Severity
	File     string // relative to the world root, forward slashes
	Line     int    // 1-based, in the file's own coordinates
	Path     string // frontmatter path, e.g. "relations[2].target"
	Msg      string
}

func (f Finding) String() string {
	var b strings.Builder
	b.WriteString(f.File)
	if f.Line > 0 {
		fmt.Fprintf(&b, ":%d", f.Line)
	}
	if f.Path != "" {
		b.WriteString(": ")
		b.WriteString(f.Path)
	}
	fmt.Fprintf(&b, ": %s [%s]", f.Msg, f.Code)
	return b.String()
}

// Findings is every fault from one pass. Loading and validating collect rather
// than stop at the first, so a writer fixes a world in one sitting instead of
// one build per mistake.
type Findings []Finding

func (fs Findings) String() string {
	lines := make([]string, 0, len(fs))
	for _, f := range fs {
		lines = append(lines, "  "+f.String())
	}
	return strings.Join(lines, "\n")
}

// Sort puts findings in a stable order: errors before warnings, then by file
// and position. Sorting keeps output identical across runs and platforms,
// which is what lets a build report be diffed.
func (fs Findings) Sort() {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		return cmp.Or(
			severityRank(a.Severity)-severityRank(b.Severity),
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.Code, b.Code),
			cmp.Compare(a.Msg, b.Msg),
		)
	})
}

// Filter returns the findings of one severity, keeping their order.
func (fs Findings) Filter(sev Severity) Findings {
	var out Findings
	for _, f := range fs {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

// Errors counts the findings that stop a build.
func (fs Findings) Errors() int {
	n := 0
	for _, f := range fs {
		if f.Severity == SeverityError {
			n++
		}
	}
	return n
}

// HasErrors reports whether the build should exit non-zero.
func (fs Findings) HasErrors() bool { return fs.Errors() > 0 }

func severityRank(s Severity) int {
	if s == SeverityError {
		return 0
	}
	return 1
}
