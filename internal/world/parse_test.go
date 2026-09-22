package world

import (
	"strings"
	"testing"
)

const kaelenFM = `id: char_kaelen_vor
type: character
name: Kaelen Vor
aliases: ["The Ashen Knight", "Vor"]
status: canon
visibility: public
lifespan: { era: third_reign, earliest: 380, latest: 419, precision: exact }
relations:
  - { type: member_of, target: fac_ashen_court, priority: 1 }
  - { type: participated_in, target: evt_siege_of_vale,
      valid_in: [{ decision: dec_siege_outcome, outcome: held }] }
beliefs:
  - { statement: stmt_orrin_betrayal, value: true, confidence: high,
      acquired_from: char_miren }
asserts:
  - { statement: stmt_orrin_betrayal, value: false, to: char_miren }
valid_in: null
`

func TestParseEntity(t *testing.T) {
	doc, fs := Parse("characters/kaelen-vor.md", file(kaelenFM, "Kaelen Vor came to the Vale as a conscript.\n"))
	noFindings(t, fs)

	if doc.Kind != KindEntity {
		t.Fatalf("kind = %q, want entity", doc.Kind)
	}
	e := doc.Entity
	if e == nil {
		t.Fatal("entity is nil")
	}

	if e.ID != "char_kaelen_vor" || e.Type != "character" || e.Name != "Kaelen Vor" {
		t.Errorf("identity fields wrong: %+v", e)
	}
	if got, want := len(e.Aliases), 2; got != want {
		t.Errorf("aliases = %d, want %d", got, want)
	}
	if e.Status != StatusCanon {
		t.Errorf("status = %q", e.Status)
	}
	if e.Visibility.Kind != VisibilityPublic {
		t.Errorf("visibility = %+v", e.Visibility)
	}

	if e.Lifespan == nil {
		t.Fatal("lifespan not parsed")
	}
	if e.Lifespan.Era != "third_reign" || *e.Lifespan.Earliest != 380 ||
		*e.Lifespan.Latest != 419 || e.Lifespan.Precision != PrecisionExact {
		t.Errorf("lifespan = %+v", e.Lifespan)
	}

	if len(e.Relations) != 2 {
		t.Fatalf("relations = %d, want 2", len(e.Relations))
	}
	if r := e.Relations[0]; r.Type != "member_of" || r.Target != "fac_ashen_court" || *r.Priority != 1 {
		t.Errorf("relations[0] = %+v", r)
	}
	if got := e.Relations[1].ValidIn; len(got) != 1 ||
		got[0].Decision != "dec_siege_outcome" || got[0].Outcome != "held" {
		t.Errorf("relations[1].valid_in = %+v", got)
	}

	if len(e.Beliefs) != 1 {
		t.Fatalf("beliefs = %d, want 1", len(e.Beliefs))
	}
	if b := e.Beliefs[0]; b.Statement != "stmt_orrin_betrayal" || b.Value == nil ||
		*b.Value != true || b.Confidence != ConfidenceHigh || b.AcquiredFrom != "char_miren" {
		t.Errorf("beliefs[0] = %+v", b)
	}

	if len(e.Asserts) != 1 {
		t.Fatalf("asserts = %d, want 1", len(e.Asserts))
	}
	if a := e.Asserts[0]; a.Value == nil || *a.Value != false || a.To != "char_miren" {
		t.Errorf("asserts[0] = %+v", a)
	}

	// valid_in: null is unconditional, which is the default and the
	// overwhelming majority. It must not become a one-element list.
	if e.ValidIn != nil {
		t.Errorf("valid_in = %+v, want nil for an unconditional entity", e.ValidIn)
	}

	if e.Body != "Kaelen Vor came to the Vale as a conscript." {
		t.Errorf("body = %q", e.Body)
	}
}

const statementFM = `id: stmt_orrin_betrayal
type: statement
subject: char_orrin
relation: betrayed
object: char_kaelen_vor
truth: false
status: canon
visibility: internal
`

func TestParseStatement(t *testing.T) {
	doc, fs := Parse("statements/orrin-betrayal.md", file(statementFM, "Contested: two accounts disagree.\n"))
	noFindings(t, fs)

	if doc.Kind != KindStatement {
		t.Fatalf("kind = %q, want statement", doc.Kind)
	}
	if doc.Entity != nil {
		t.Error("a statement must not also parse as an entity")
	}
	s := doc.Statement
	if s == nil {
		t.Fatal("statement is nil")
	}
	if s.ID != "stmt_orrin_betrayal" || s.Subject != "char_orrin" ||
		s.Relation != "betrayed" || s.Object != "char_kaelen_vor" {
		t.Errorf("triple wrong: %+v", s)
	}
	if s.Truth != TruthFalse {
		t.Errorf("truth = %q, want false", s.Truth)
	}
	if s.Visibility.Kind != VisibilityInternal {
		t.Errorf("visibility = %+v", s.Visibility)
	}
}

// TestParseRoutesOnFrontmatter is the discriminator rule: `type: statement`
// decides what a file is, not the directory it sits in. The directory is a
// human convention, and disagreeing with it is a warning the validator raises
// — never a reason to parse a file as something it did not declare.
func TestParseRoutesOnFrontmatter(t *testing.T) {
	doc, fs := Parse("characters/misfiled.md", file(statementFM, ""))
	noFindings(t, fs)
	if doc.Kind != KindStatement {
		t.Errorf("kind = %q; a file in characters/ declaring type: statement is a statement", doc.Kind)
	}

	doc, fs = Parse("statements/misfiled.md", file(kaelenFM, ""))
	noFindings(t, fs)
	if doc.Kind != KindEntity {
		t.Errorf("kind = %q; a file in statements/ declaring type: character is an entity", doc.Kind)
	}
}

func TestParseFraming(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Code
	}{
		{"no opening fence", "id: char_x\ntype: character\n", CodeNoFrontmatter},
		{"prose before fence", "Hello.\n---\nid: char_x\n---\n", CodeNoFrontmatter},
		{"unterminated", "---\nid: char_x\ntype: character\n", CodeUnterminatedFrontmatter},
		{"empty block", "---\n---\n\nbody\n", CodeEmptyFrontmatter},
		{"empty file", "", CodeNoFrontmatter},
		{"broken yaml", "---\nid: [char_x\n---\n", CodeParse},
		{"unknown field", "---\nid: char_x\ntype: character\nnmae: typo\n---\n", CodeUnknownField},
		{"body field is not authorable", "---\nid: char_x\ntype: character\nbody: sneaky\n---\n", CodeUnknownField},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fs := Parse("characters/x.md", []byte(tt.in))
			wantCodes(t, fs, tt.want)
		})
	}
}

// TestParseWindowsFraming is not incidental coverage: Windows is the primary
// platform, and an editor that writes CRLF or a BOM must not produce a file
// the toolchain refuses.
func TestParseWindowsFraming(t *testing.T) {
	crlf := "---\r\nid: char_x\r\ntype: character\r\nname: X\r\nstatus: canon\r\nvisibility: public\r\n---\r\n\r\nProse.\r\nMore.\r\n"
	doc, fs := Parse("characters/x.md", []byte(crlf))
	noFindings(t, fs)
	if doc.Entity.ID != "char_x" {
		t.Errorf("id = %q", doc.Entity.ID)
	}
	// The body is normalised to LF so a golden does not change with the
	// checkout's line endings.
	if doc.Entity.Body != "Prose.\nMore." {
		t.Errorf("body = %q", doc.Entity.Body)
	}

	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("---\nid: char_x\ntype: character\n---\n")...)
	doc, fs = Parse("characters/x.md", withBOM)
	noFindings(t, fs)
	if doc.Entity.ID != "char_x" {
		t.Errorf("BOM: id = %q", doc.Entity.ID)
	}
}

func TestParseBlankLinesBeforeFence(t *testing.T) {
	in := "\n\n---\nid: char_x\ntype: character\n---\nbody\n"
	doc, fs := Parse("characters/x.md", []byte(in))
	noFindings(t, fs)
	if doc.Entity.ID != "char_x" {
		t.Errorf("id = %q", doc.Entity.ID)
	}
	// Lines are still counted from the top of the file, blank leader included.
	if got := doc.Entity.Source.LineOf("id"); got != 4 {
		t.Errorf("LineOf(id) = %d, want 4", got)
	}
}

// TestSourceLines is the reason the parser decodes twice. A validator that
// says "relations[2].target" without a line makes a writer count list items by
// hand, and they will miscount.
func TestSourceLines(t *testing.T) {
	doc, fs := Parse("characters/kaelen-vor.md", file(kaelenFM, "prose\n"))
	noFindings(t, fs)
	src := doc.Entity.Source

	tests := []struct {
		path string
		want int
	}{
		{"id", 2},
		{"type", 3},
		{"name", 4},
		{"aliases", 5},
		{"aliases[1]", 5},
		{"status", 6},
		{"visibility", 7},
		{"lifespan", 8},
		{"lifespan.era", 8},
		{"lifespan.precision", 8},
		{"relations", 9},
		{"relations[0]", 10},
		{"relations[0].target", 10},
		{"relations[1]", 11},
		{"relations[1].valid_in[0].outcome", 12},
		{"beliefs[0].statement", 14},
		{"beliefs[0].acquired_from", 15},
		{"asserts[0].to", 17},
		{"valid_in", 18},
	}
	for _, tt := range tests {
		if got := src.LineOf(tt.path); got != tt.want {
			t.Errorf("LineOf(%q) = %d, want %d", tt.path, got, tt.want)
		}
	}

	// An unmapped path falls back to its nearest mapped ancestor, and finally
	// to the head of the frontmatter block — never to zero, because a finding
	// with no line is worse than one that is merely coarse.
	if got := src.LineOf("relations[0].note"); got != 10 {
		t.Errorf("LineOf of a missing field = %d, want its parent's line 10", got)
	}
	if got := src.LineOf("nothing.like.this"); got != src.Line {
		t.Errorf("LineOf of an unknown path = %d, want the block head %d", got, src.Line)
	}
	if src.Line != 2 {
		t.Errorf("Source.Line = %d, want the first frontmatter line, 2", src.Line)
	}
	if src.File != "characters/kaelen-vor.md" {
		t.Errorf("Source.File = %q", src.File)
	}
}

// TestParseYAMLFindingsCarryLines checks that a YAML-level fault is located in
// the file's own coordinates, not the frontmatter block's.
func TestParseYAMLFindingsCarryLines(t *testing.T) {
	in := "---\nid: char_x\ntype: character\nnmae: typo\n---\n"
	_, fs := Parse("characters/x.md", []byte(in))
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1: %v", len(fs), Findings(fs))
	}
	if fs[0].Line != 4 {
		t.Errorf("line = %d, want 4 (the typo's line in the file)", fs[0].Line)
	}
	if !strings.Contains(fs[0].Msg, "nmae") {
		t.Errorf("message should name the offending field: %q", fs[0].Msg)
	}
}

func TestParseTypeSpecificFields(t *testing.T) {
	fm := "id: evt_x\ntype: event\nname: X\nstatus: canon\nvisibility: public\n" +
		"date: { era: third_reign, earliest: 412, latest: 418, precision: approximate }\n"
	doc, fs := Parse("events/x.md", file(fm, ""))
	noFindings(t, fs)
	if doc.Entity.Date == nil || doc.Entity.Date.Precision != PrecisionApproximate {
		t.Errorf("date = %+v", doc.Entity.Date)
	}

	fm = "id: dec_siege\ntype: decision\nname: Siege outcome\nstatus: canon\nvisibility: public\n" +
		"outcomes: [held, fell, betrayed]\n"
	doc, fs = Parse("decisions/siege.md", file(fm, ""))
	noFindings(t, fs)
	if got := doc.Entity.Outcomes; len(got) != 3 || got[2] != "betrayed" {
		t.Errorf("outcomes = %v", got)
	}
}

// TestParseEmptyBody keeps the "entities with no prose body" metric honest:
// whitespace is not a body.
func TestParseEmptyBody(t *testing.T) {
	for _, body := range []string{"", "\n", "   \n\n\t\n"} {
		doc, fs := Parse("characters/x.md", file("id: char_x\ntype: character\n", body))
		noFindings(t, fs)
		if doc.Entity.Body != "" {
			t.Errorf("body %q parsed to %q, want empty", body, doc.Entity.Body)
		}
	}
}
