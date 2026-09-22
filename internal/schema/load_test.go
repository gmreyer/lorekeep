package schema

import (
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

func TestLoadDirFixture(t *testing.T) {
	p, err := LoadDir("testdata/project-ok")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	if p.Name != "nightfall" {
		t.Errorf("Name = %q, want %q", p.Name, "nightfall")
	}
	if p.Version != "0.2.0" {
		t.Errorf("Version = %q, want %q", p.Version, "0.2.0")
	}
	if p.CoreVersion != "0.1.0" {
		t.Errorf("CoreVersion = %q, want %q", p.CoreVersion, "0.1.0")
	}
	if p.Source != SourceProject {
		t.Errorf("Source = %q, want %q", p.Source, SourceProject)
	}

	// The project pack adds one type and one group, and declares no roles.
	if diff := cmp.Diff([]EntityType{
		{Name: "language", Description: "A tongue or a script."},
	}, p.Types); diff != "" {
		t.Errorf("Types mismatch (-want +got):\n%s", diff)
	}
	if len(p.Roles) != 0 {
		t.Errorf("project pack declares %d role(s); roles are core-owned", len(p.Roles))
	}

	// Spot-check the shape of a relation as authored: inverse is a name only,
	// and symmetry is a flag rather than a self-referential inverse.
	sworn, ok := p.Relation("sworn_to")
	if !ok {
		t.Fatal("fixture does not declare sworn_to")
	}
	if diff := cmp.Diff(Relation{
		Name:        "sworn_to",
		Domain:      []string{"character"},
		Range:       []string{"character", "faction"},
		Inverse:     "has_sworn",
		Role:        "membership",
		Description: "Bound by oath of service to.",
	}, *sworn); diff != "" {
		t.Errorf("sworn_to mismatch (-want +got):\n%s", diff)
	}

	allied, ok := p.Relation("allied_with")
	if !ok {
		t.Fatal("fixture does not declare allied_with")
	}
	if !allied.Symmetric {
		t.Error("allied_with should be symmetric")
	}
	if allied.Inverse != "" {
		t.Errorf("allied_with declares inverse %q; a symmetric relation has none", allied.Inverse)
	}
	if allied.Role != "" {
		t.Errorf("allied_with role = %q, want none", allied.Role)
	}

	// Era order is list order.
	if diff := cmp.Diff([]Era{
		{Key: "founding", Name: "The Founding", Description: "Before the Courts."},
		{Key: "long_silence", Name: "The Long Silence", Aliases: []string{"the Silence"}},
		{Key: "third_reign", Name: "The Third Reign", Description: "The era of the Ashen Court."},
	}, p.Eras); diff != "" {
		t.Errorf("Eras mismatch (-want +got):\n%s", diff)
	}
}

// A pack directory need not carry every content file; a missing one is an
// empty section. Only pack.yaml is required, because the core version pin
// must never be absent by accident.
func TestLoadMissingContentFilesIsEmpty(t *testing.T) {
	p, err := LoadFS(fsWith(nil))
	if err != nil {
		t.Fatalf("LoadFS on a pack with only pack.yaml: %v", err)
	}
	if len(p.Types) != 0 || len(p.Relations) != 0 || len(p.Eras) != 0 || len(p.Groups) != 0 {
		t.Errorf("empty pack is not empty: %+v", p)
	}
}

func TestLoadStructuralErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []Code
	}{
		{
			name:  "no pack.yaml",
			files: nil, // built inline below
			want:  []Code{CodeNotAPack},
		},
		{
			name:  "malformed yaml",
			files: map[string]string{"relations.yaml": "relations:\n  - name: x\n   domain: ["},
			want:  []Code{CodeParse},
		},
		{
			name: "unknown field",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [character]
    revrese: y
`},
			want: []Code{CodeUnknownField},
		},
		{
			name:  "pack.yaml without a version",
			files: map[string]string{"pack.yaml": "name: test\ncore_version: 0.1.0\n"},
			want:  []Code{CodeMissingField},
		},
		{
			name:  "pack.yaml without a core_version pin",
			files: map[string]string{"pack.yaml": "name: test\nversion: 0.1.0\n"},
			want:  []Code{CodeMissingField},
		},
		{
			name:  "version is not semver",
			files: map[string]string{"pack.yaml": "name: test\nversion: v1\ncore_version: 0.1.0\n"},
			want:  []Code{CodeBadVersion},
		},
		{
			name: "relation without a name",
			files: map[string]string{"relations.yaml": `
relations:
  - domain: [character]
    range: [character]
`},
			want: []Code{CodeMissingField},
		},
		{
			name: "relation without a domain or a range",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
`},
			want: []Code{CodeMissingField},
		},
		{
			name: "symmetric relation declaring an inverse",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [character]
    symmetric: true
    inverse: y
`},
			want: []Code{CodeSymmetricInverse},
		},
		{
			name: "symmetric relation whose domain and range differ",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [faction]
    symmetric: true
`},
			want: []Code{CodeSymmetricDomain},
		},
		{
			name: "relation that is its own inverse without being symmetric",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [character]
    inverse: x
`},
			want: []Code{CodeSelfInverse},
		},
		{
			name: "duplicate relation name within one pack",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [character]
  - name: x
    domain: [faction]
    range: [faction]
`},
			want: []Code{CodeDuplicateName},
		},
		{
			name: "two relations claiming the same inverse name",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [character]
    inverse: z
  - name: y
    domain: [faction]
    range: [faction]
    inverse: z
`},
			want: []Code{CodeDuplicateName},
		},
		{
			name: "an inverse colliding with a relation name in the same pack",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
    domain: [character]
    range: [character]
    inverse: y
  - name: y
    domain: [faction]
    range: [faction]
`},
			want: []Code{CodeDuplicateName},
		},
		{
			name: "names colliding case-insensitively",
			files: map[string]string{"relations.yaml": `
relations:
  - name: member_of
    domain: [character]
    range: [faction]
  - name: Member_Of
    domain: [character]
    range: [faction]
`},
			want: []Code{CodeCaseCollision},
		},
		{
			name:  "duplicate era key",
			files: map[string]string{"eras.yaml": "eras:\n  - {key: a, name: A}\n  - {key: a, name: Also A}\n"},
			want:  []Code{CodeDuplicateName},
		},
		{
			name:  "era without a key",
			files: map[string]string{"eras.yaml": "eras:\n  - {name: A}\n"},
			want:  []Code{CodeMissingField},
		},
		{
			name:  "entity type colliding with a group name",
			files: map[string]string{"entity-types.yaml": "types:\n  - {name: x}\ngroups:\n  - {name: x, includes: [x]}\n"},
			want:  []Code{CodeDuplicateName},
		},
		{
			name: "every error is reported, not just the first",
			files: map[string]string{"relations.yaml": `
relations:
  - name: x
  - name: y
`},
			want: []Code{CodeMissingField},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fsys fstest.MapFS
			if tt.files == nil {
				fsys = fstest.MapFS{"readme.txt": &fstest.MapFile{Data: []byte("not a pack")}}
			} else {
				fsys = fsWith(tt.files)
			}
			_, err := LoadFS(fsys)
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			wantCodes(t, err, tt.want...)
		})
	}
}

// Errors accumulate. A pack with three separate faults reports three, so a
// writer fixes them in one pass rather than one build per mistake.
func TestLoadReportsEveryError(t *testing.T) {
	_, err := LoadFS(fsWith(map[string]string{"relations.yaml": `
relations:
  - name: a
  - name: b
  - name: c
`}))
	var errs Errors
	if !asErrors(err, &errs) {
		t.Fatalf("expected schema.Errors, got %T", err)
	}
	if len(errs) != 3 {
		t.Errorf("got %d errors, want 3: %v", len(errs), errs)
	}
}

// TestReservedTypeName guards the discriminator that routes a world file to
// the statement loader.
//
// A Statement is not an eighth entity type: it may never stand at either end
// of a relation. Because domain and range admit only declared entity types,
// keeping "statement" out of Pack.Types is what makes that structural rather
// than a rule someone has to remember — so a pack claiming the name has to
// fail here, in the pack, rather than later and more confusingly in a world.
func TestReservedTypeName(t *testing.T) {
	_, err := LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: statement
`}))
	wantCodes(t, err, CodeReservedName)

	_, err = LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: language
groups:
  - name: statement
    includes: [language]
`}))
	wantCodes(t, err, CodeReservedName)

	// Case folding too: Windows would treat Statement and statement as one
	// name, and so does the rest of this loader.
	_, err = LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: Statement
`}))
	wantCodes(t, err, CodeReservedName)

	// Nothing else is reserved. A world that wants a "claim" or a "rumour"
	// entity type gets one.
	_, err = LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: rumour
  - name: claim
`}))
	if err != nil {
		t.Errorf("unreserved names should load: %v", err)
	}
}

// TestLoadActs covers the spoiler-act list: the ordered spine of what a reader
// has been allowed to see.
//
// Order is list order, as it is for eras, because spoiler:act2 must imply that
// an act-1 reader is excluded. That is an ordinal comparison, so the list has
// to be ordered and there is no sequence field to disagree with it.
func TestLoadActs(t *testing.T) {
	p, err := LoadFS(fsWith(map[string]string{"acts.yaml": `
acts:
  - key: act1
    name: The Vale
  - key: act2
    name: The Long Road
  - key: act3
`}))
	if err != nil {
		t.Fatalf("loading acts: %v", err)
	}
	if len(p.Acts) != 3 {
		t.Fatalf("acts = %d, want 3", len(p.Acts))
	}
	for i, key := range []string{"act1", "act2", "act3"} {
		ord, ok := p.ActOrdinal(key)
		if !ok {
			t.Errorf("ActOrdinal(%q) missing", key)
			continue
		}
		if ord != i {
			t.Errorf("ActOrdinal(%q) = %d, want %d", key, ord, i)
		}
	}
	if _, ok := p.ActOrdinal("act4"); ok {
		t.Error("ActOrdinal resolved an act that is not declared")
	}
}

func TestLoadActErrors(t *testing.T) {
	_, err := LoadFS(fsWith(map[string]string{"acts.yaml": "acts:\n  - name: nameless\n"}))
	wantCodes(t, err, CodeMissingField)

	_, err = LoadFS(fsWith(map[string]string{"acts.yaml": "acts:\n  - {key: act1}\n  - {key: act1}\n"}))
	wantCodes(t, err, CodeDuplicateName)

	// Case folding, for the same reason it applies everywhere else here.
	_, err = LoadFS(fsWith(map[string]string{"acts.yaml": "acts:\n  - {key: act1}\n  - {key: Act1}\n"}))
	wantCodes(t, err, CodeCaseCollision)

	_, err = LoadFS(fsWith(map[string]string{"acts.yaml": "acts:\n  - {key: act1, sequence: 1}\n"}))
	wantCodes(t, err, CodeUnknownField)
}

// A missing acts.yaml is an empty section, like every other content file: a
// world with no spoiler gating is a legitimate world.
func TestLoadWithoutActs(t *testing.T) {
	p, err := LoadFS(fsWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Acts) != 0 {
		t.Errorf("acts = %v, want none", p.Acts)
	}
}
