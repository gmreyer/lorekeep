package validate

import (
	"testing"

	"github.com/gmreyer/lore-core/internal/schema"
	"github.com/gmreyer/lore-core/internal/world"
)

// TestFixtureIsClean is the baseline every other case rests on. If the fixture
// world reported anything, a case could not tell its own fault apart from the
// fixture's.
func TestFixtureIsClean(t *testing.T) {
	fs := check(t, nil)
	if len(fs) != 0 {
		t.Fatalf("the fixture world should validate clean, got %d findings:\n%v", len(fs), fs)
	}
}

// TestStatementTypeIsReserved closes the loop between the two packages that
// have to agree about the discriminator without either importing the other.
//
// world routes a file to the statement loader on `type: statement`; schema
// refuses to let any pack declare that name as an entity type. If those two
// facts ever drift apart, a project could declare a statement entity type and
// statements would become legal relation endpoints.
func TestStatementTypeIsReserved(t *testing.T) {
	core, err := schema.Core()
	if err != nil {
		t.Fatal(err)
	}
	if core.HasType(world.StatementType) {
		t.Fatalf("%q is a declared entity type; a statement could then be a relation endpoint", world.StatementType)
	}
	for _, rel := range core.Relations {
		if core.AllowsDomain(rel.Name, world.StatementType) || core.AllowsRange(rel.Name, world.StatementType) {
			t.Errorf("relation %q admits %q at an endpoint", rel.Name, world.StatementType)
		}
	}
}

func TestBlockingErrors(t *testing.T) {
	tests := []struct {
		name    string
		overlay map[string]string
		want    []world.Code
	}{
		{
			name: "dangling relation target",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: originates_from, target: loc_nowhere }`),
			},
			want: []world.Code{CodeDangling},
		},
		{
			name: "unknown relation type",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: betrayed, target: char_kaelen_vor }`),
			},
			want: []world.Code{CodeUnknownRelation},
		},
		{
			name: "unknown entity type",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: deity
name: Orrin
status: canon
visibility: public`),
			},
			want: []world.Code{CodeUnknownEntityType},
		},
		{
			name: "unknown era",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
lifespan: { era: fourth_reign, earliest: 378, latest: 414 }`),
			},
			want: []world.Code{CodeUnknownEra},
		},
		{
			// member_of runs character|faction -> faction. A location may not
			// stand at its left-hand end.
			name: "domain violation",
			overlay: map[string]string{
				"world/locations/vale-of-orrin.md": entityFile(`id: loc_vale_of_orrin
type: location
name: The Vale of Orrin
status: canon
visibility: public
relations:
  - { type: member_of, target: fac_ashen_court }`),
			},
			want: []world.Code{CodeDomainViolation},
		},
		{
			name: "range violation",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: member_of, target: char_kaelen_vor }`),
			},
			want: []world.Code{CodeRangeViolation},
		},
		{
			// has_member is the declared inverse of member_of. Inverses are
			// derived by the index and never authored, so using one as a
			// relation type is the error that keeps that rule true.
			name: "authored inverse name",
			overlay: map[string]string{
				"world/factions/ashen-court.md": entityFile(`id: fac_ashen_court
type: faction
name: The Ashen Court
status: canon
visibility: public
relations:
  - { type: has_member, target: char_kaelen_vor }
beliefs:
  - { statement: stmt_orrin_oath, value: true, confidence: medium }`),
			},
			want: []world.Code{CodeInverseAuthored},
		},
		{
			name: "valid_in names an outcome that does not exist",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: participated_in, target: evt_siege_of_vale,
      valid_in: [{ decision: dec_siege_outcome, outcome: razed }] }`),
			},
			want: []world.Code{CodeUnknownOutcome},
		},
		{
			name: "valid_in names something that is not a decision",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
valid_in: [{ decision: char_miren, outcome: held }]`),
			},
			want: []world.Code{CodeWrongKind},
		},
		{
			name: "belief points at a statement that does not exist",
			overlay: map[string]string{
				"world/characters/kaelen-vor.md": entityFile(`id: char_kaelen_vor
type: character
name: Kaelen Vor
status: canon
visibility: public
beliefs:
  - { statement: stmt_nothing, value: true }`),
			},
			want: []world.Code{CodeDangling},
		},
		{
			name: "belief points at an entity rather than a statement",
			overlay: map[string]string{
				"world/characters/kaelen-vor.md": entityFile(`id: char_kaelen_vor
type: character
name: Kaelen Vor
status: canon
visibility: public
beliefs:
  - { statement: char_miren, value: true }`),
			},
			want: []world.Code{CodeWrongKind},
		},
		{
			name: "a statement standing at a relation endpoint",
			overlay: map[string]string{
				"world/characters/kaelen-vor.md": entityFile(`id: char_kaelen_vor
type: character
name: Kaelen Vor
status: canon
visibility: public
relations:
  - { type: originates_from, target: stmt_orrin_oath }`),
			},
			want: []world.Code{CodeStatementEndpoint},
		},
		{
			name: "duplicate id",
			overlay: map[string]string{
				"world/characters/twin.md": entityFile(`id: char_miren
type: character
name: Miren Again
status: canon
visibility: public`),
			},
			want: []world.Code{CodeDuplicateID},
		},
		{
			// The charset rule makes an id case collision unreachable in
			// practice — an uppercase id is already illegal — so this case
			// reports both. The rule is kept as the backstop the spec asks
			// for, and because ids are opaque and the charset may one day
			// widen.
			name: "ids colliding when case is ignored",
			overlay: map[string]string{
				"world/characters/twin.md": entityFile(`id: Char_Miren
type: character
name: Miren Again
status: canon
visibility: public`),
			},
			want: []world.Code{CodeBadID, CodeCaseCollision},
		},
		{
			name: "id outside the permitted charset",
			overlay: map[string]string{
				"world/concepts/oathbinding.md": entityFile(`id: con-oathbinding
type: concept
name: Oathbinding
status: canon
visibility: public`),
			},
			want: []world.Code{CodeBadID},
		},
		{
			name: "unknown spoiler act",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: spoiler:act9`),
			},
			want: []world.Code{CodeUnknownAct},
		},
		{
			name: "malformed visibility",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: secret`),
			},
			want: []world.Code{CodeBadEnum},
		},
		{
			name: "invalid status",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: published
visibility: public`),
			},
			want: []world.Code{CodeBadEnum},
		},
		{
			name: "missing required fields",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character`),
			},
			want: []world.Code{CodeMissingField},
		},
		{
			name: "a field belonging to another type",
			overlay: map[string]string{
				"world/factions/ashen-court.md": entityFile(`id: fac_ashen_court
type: faction
name: The Ashen Court
status: canon
visibility: public
lifespan: { era: third_reign, earliest: 300 }
beliefs:
  - { statement: stmt_orrin_oath, value: true, confidence: medium }`),
			},
			want: []world.Code{CodeFieldOnWrongType},
		},
		{
			name: "a decision declaring no outcomes",
			overlay: map[string]string{
				"world/decisions/siege-outcome.md": entityFile(`id: dec_siege_outcome
type: decision
name: The outcome of the Siege of Vale
status: canon
visibility: public`),
			},
			want: []world.Code{CodeMissingField, CodeUnknownOutcome},
		},
		{
			name: "an outcome key outside the permitted charset",
			overlay: map[string]string{
				"world/decisions/siege-outcome.md": entityFile(`id: dec_siege_outcome
type: decision
name: The outcome of the Siege of Vale
status: canon
visibility: public
outcomes: [held, fell, betrayed, "Razed!"]`),
			},
			want: []world.Code{CodeBadID},
		},
		{
			name: "a statement whose triple violates the relation it reifies",
			overlay: map[string]string{
				"world/statements/orrin-oath.md": entityFile(`id: stmt_orrin_oath
type: statement
name: Orrin was sworn to the Ashen Court
subject: loc_vale_of_orrin
relation: sworn_to
object: fac_ashen_court
truth: false
status: canon
visibility: internal`),
			},
			want: []world.Code{CodeDomainViolation},
		},
		{
			name: "a statement whose relation is not declared",
			overlay: map[string]string{
				"world/statements/orrin-oath.md": entityFile(`id: stmt_orrin_oath
type: statement
subject: char_orrin
relation: betrayed
object: fac_ashen_court
truth: false
status: canon
visibility: internal`),
			},
			want: []world.Code{CodeUnknownRelation},
		},
		{
			name: "a statement with no truth value",
			overlay: map[string]string{
				"world/statements/orrin-oath.md": entityFile(`id: stmt_orrin_oath
type: statement
subject: char_orrin
relation: sworn_to
object: fac_ashen_court
status: canon
visibility: internal`),
			},
			want: []world.Code{CodeMissingField},
		},
		{
			name: "a belief with no explicit value",
			overlay: map[string]string{
				"world/factions/ashen-court.md": entityFile(`id: fac_ashen_court
type: faction
name: The Ashen Court
status: canon
visibility: public
beliefs:
  - { statement: stmt_orrin_oath, confidence: medium }`),
			},
			want: []world.Code{CodeMissingField},
		},
		{
			name: "an unknown confidence",
			overlay: map[string]string{
				"world/factions/ashen-court.md": entityFile(`id: fac_ashen_court
type: faction
name: The Ashen Court
status: canon
visibility: public
beliefs:
  - { statement: stmt_orrin_oath, value: true, confidence: certain }`),
			},
			want: []world.Code{CodeBadEnum},
		},
		{
			name: "acquired_from naming nobody",
			overlay: map[string]string{
				"world/characters/kaelen-vor.md": entityFile(`id: char_kaelen_vor
type: character
name: Kaelen Vor
status: canon
visibility: public
beliefs:
  - { statement: stmt_orrin_oath, value: true, acquired_from: char_nobody }`),
			},
			want: []world.Code{CodeDangling},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := check(t, tt.overlay)
			wantErrors(t, fs, tt.want...)
			if !fs.HasErrors() {
				t.Error("the build must exit non-zero on this world")
			}
		})
	}
}

// TestFilenamesCollidingWhenCaseIsIgnored cannot be written as a file on disk:
// Windows is precisely the platform that cannot hold both names, which is why
// the rule exists. The collision is built in memory instead.
func TestFilenamesCollidingWhenCaseIsIgnored(t *testing.T) {
	fs := checkMutated(t, func(w *world.World) {
		e, ok := w.Entity("char_orrin")
		if !ok {
			t.Fatal("char_orrin missing from the fixture")
		}
		e.Source.File = "characters/Kaelen-Vor.md"
	})
	wantErrors(t, fs, CodeFileCaseCollision)
}

func TestWarnings(t *testing.T) {
	tests := []struct {
		name    string
		overlay map[string]string
		want    []world.Code
	}{
		{
			name: "a character at an event outside their lifespan",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
lifespan: { era: third_reign, earliest: 300, latest: 350 }
relations:
  - { type: participated_in, target: evt_siege_of_vale,
      valid_in: [{ decision: dec_siege_outcome, outcome: betrayed }] }`),
			},
			want: []world.Code{CodeLifespanMismatch},
		},
		{
			// The same check, through a project relation named nothing like
			// the core one. Views and rules bind to roles, never to relation
			// names, or the system stops being reusable.
			name: "the lifespan check binds to the participation role, not a name",
			overlay: map[string]string{
				"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
lifespan: { era: third_reign, earliest: 300, latest: 350 }
relations:
  - { type: witnessed, target: evt_siege_of_vale,
      valid_in: [{ decision: dec_siege_outcome, outcome: betrayed }] }`),
			},
			want: []world.Code{CodeLifespanMismatch},
		},
		{
			name: "an orphan entity",
			overlay: map[string]string{
				"world/concepts/oathbinding.md": entityFile(`id: con_oathbinding
type: concept
name: Oathbinding
status: canon
visibility: public`),
			},
			want: []world.Code{CodeOrphan},
		},
		{
			name: "a canon entity depending on a draft one",
			overlay: map[string]string{
				"world/locations/vale-of-orrin.md": entityFile(`id: loc_vale_of_orrin
type: location
name: The Vale of Orrin
status: draft
visibility: public`),
			},
			want: []world.Code{CodeCanonDependsOnDraft},
		},
		{
			name: "a statement nobody believes or asserts",
			overlay: map[string]string{
				"world/statements/court-membership.md": entityFile(`id: stmt_court_membership
type: statement
name: Miren belongs to the Ashen Court
subject: char_miren
relation: member_of
object: fac_ashen_court
truth: true
status: canon
visibility: public`),
			},
			want: []world.Code{CodeUnbelievedStatement},
		},
		{
			name: "a directory that disagrees with the type",
			overlay: map[string]string{
				"world/factions/ashen-court.md": "",
				"world/characters/ashen-court.md": entityFile(`id: fac_ashen_court
type: faction
name: The Ashen Court
status: canon
visibility: public
relations:
  - { type: participated_in, target: evt_siege_of_vale }
beliefs:
  - { statement: stmt_orrin_oath, value: true, confidence: medium }`),
			},
			want: []world.Code{CodeDirectoryMismatch},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := check(t, tt.overlay)
			wantWarnings(t, fs, tt.want...)
		})
	}
}

// TestOrphanReferences pins which edges make their author referenced.
//
// An edge whose relation is symmetric or has a derived inverse is inbound at
// both ends: the inverse is an edge the graph holds even though nobody wrote
// it. Only a one-way relation, which in the fixture pack is mentions alone,
// leaves its author unreferenced.
func TestOrphanReferences(t *testing.T) {
	ilse := func(status, relations string) string {
		return entityFile(`id: char_ilse_marrow
type: character
name: Ilse Marrow
status: ` + status + `
visibility: public
relations:
` + relations)
	}
	tomas := func(status, relations string) string {
		fm := `id: char_tomas_marrow
type: character
name: Tomas Marrow
status: ` + status + `
visibility: public
`
		if relations != "" {
			fm += "relations:\n" + relations
		}
		return entityFile(fm)
	}

	tests := []struct {
		name    string
		overlay map[string]string
		want    []world.Code
	}{
		{
			name: "a one-sided symmetric edge references both ends",
			overlay: map[string]string{
				"world/characters/ilse-marrow.md":  ilse("canon", "  - { type: sibling_of, target: char_tomas_marrow }"),
				"world/characters/tomas-marrow.md": tomas("canon", ""),
			},
		},
		{
			name: "a project symmetric relation references both ends",
			overlay: map[string]string{
				"world/factions/grey-hand.md": entityFile(`id: fac_grey_hand
type: faction
name: The Grey Hand
status: canon
visibility: public
relations:
  - { type: allied_with, target: fac_ashen_court }`),
			},
		},
		{
			name: "an edge with a derived inverse references its author",
			overlay: map[string]string{
				"world/characters/ilse-marrow.md": ilse("canon", "  - { type: member_of, target: fac_ashen_court }"),
			},
		},
		{
			name: "an author of one-way edges only is still an orphan",
			overlay: map[string]string{
				"world/concepts/oathbinding.md": entityFile(`id: con_oathbinding
type: concept
name: Oathbinding
status: canon
visibility: public
relations:
  - { type: mentions, target: fac_ashen_court }`),
			},
			want: []world.Code{CodeOrphan},
		},
		{
			// Only canon contributes references, in either direction: a draft
			// sibling cannot keep a canon entity looking connected.
			name: "a symmetric edge from a draft references neither end",
			overlay: map[string]string{
				"world/characters/ilse-marrow.md":  ilse("draft", "  - { type: sibling_of, target: char_tomas_marrow }"),
				"world/characters/tomas-marrow.md": tomas("canon", ""),
			},
			want: []world.Code{CodeOrphan},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := check(t, tt.overlay)
			wantWarnings(t, fs, tt.want...)
		})
	}
}

// TestUnknownTypeSuppressesTheDirectoryWarning: an entity whose type is not
// declared has one real problem, and a directory warning derived from that
// same unknown type is noise piled on top of it.
func TestUnknownTypeSuppressesTheDirectoryWarning(t *testing.T) {
	fs := check(t, map[string]string{
		"world/characters/orrin.md": entityFile(`id: char_orrin
type: deity
name: Orrin
status: canon
visibility: public`),
	})
	for _, f := range fs {
		if f.Code == CodeDirectoryMismatch {
			t.Errorf("unexpected directory warning alongside an unknown type: %v", f)
		}
	}
}

// TestFindingsAreLocated: every finding must name a file and a line, because a
// finding a writer cannot navigate to is a finding they will not act on.
func TestFindingsAreLocated(t *testing.T) {
	fs := check(t, map[string]string{
		"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: originates_from, target: loc_vale_of_orrin }
  - { type: originates_from, target: loc_nowhere }`),
	})
	var found bool
	for _, f := range fs {
		if f.Code != CodeDangling {
			continue
		}
		found = true
		if f.Path != "relations[1].target" {
			t.Errorf("path = %q, want relations[1].target", f.Path)
		}
		// The second list entry is on the eighth line of the file.
		if f.Line != 9 {
			t.Errorf("line = %d, want 9", f.Line)
		}
		if f.File != "characters/orrin.md" {
			t.Errorf("file = %q", f.File)
		}
	}
	if !found {
		t.Fatalf("no dangling finding: %v", fs)
	}
}

// TestEveryFaultInOnePass: a writer should fix a world in one sitting, not one
// build per mistake.
func TestEveryFaultInOnePass(t *testing.T) {
	fs := check(t, map[string]string{
		"world/characters/orrin.md": entityFile(`id: char_orrin
type: character
name: Orrin
status: published
visibility: spoiler:act9
lifespan: { era: fourth_reign, earliest: 378 }
relations:
  - { type: originates_from, target: loc_nowhere }`),
	})
	wantErrors(t, fs, CodeBadEnum, CodeUnknownAct, CodeUnknownEra, CodeDangling)
}
