package schema

import (
	"slices"
	"testing"
)

func TestMergeFixtureOverCore(t *testing.T) {
	p := mustLoadFixture(t)

	if p.Source != SourceMerged {
		t.Errorf("Source = %q, want %q", p.Source, SourceMerged)
	}
	// The merged pack keeps the project's identity: it is the world's schema,
	// with core folded in.
	if p.Name != "nightfall" {
		t.Errorf("Name = %q, want %q", p.Name, "nightfall")
	}

	// Core types plus the project's one addition.
	for _, want := range []string{"character", "faction", "location", "event",
		"object", "concept", "decision", "language"} {
		if !p.HasType(want) {
			t.Errorf("merged pack is missing type %q", want)
		}
	}
	if len(p.Types) != 8 {
		t.Errorf("merged pack has %d types, want 8", len(p.Types))
	}

	// Core relations and project relations are both present.
	for _, want := range []string{"member_of", "located_in", "occurred_at",
		"mentions", "allied_with", "sworn_to", "part_of", "speaks"} {
		if _, ok := p.Relation(want); !ok {
			t.Errorf("merged pack is missing relation %q", want)
		}
	}

	// Roles come from core alone, and the project's relations bind to them.
	if !p.HasRole("placement") {
		t.Error("merged pack is missing role placement")
	}
	var containment []string
	for _, r := range p.RelationsWithRole("containment") {
		containment = append(containment, r.Name)
	}
	slices.Sort(containment)
	if !slices.Equal(containment, []string{"located_in", "part_of"}) {
		t.Errorf("containment relations = %v, want [located_in part_of]", containment)
	}

	// Eras come from the project alone, ordered by list order.
	for i, key := range []string{"founding", "long_silence", "third_reign"} {
		got, ok := p.EraOrdinal(key)
		if !ok {
			t.Errorf("era %q missing from merged pack", key)
			continue
		}
		if got != i {
			t.Errorf("era %q ordinal = %d, want %d", key, got, i)
		}
	}
	if _, ok := p.EraOrdinal("fourth_reign"); ok {
		t.Error("EraOrdinal reported an era that is not declared")
	}
}

// Matching versions merge silently; a newer core carries a notice rather than
// an error, because the project still loads and the rename map is the tool
// for the upgrade.
func TestMergeVersionSkew(t *testing.T) {
	tests := []struct {
		name        string
		coreVersion string
		pin         string
		wantErr     []Code
		wantNotice  bool
	}{
		{name: "exact match", coreVersion: "0.1.0", pin: "0.1.0"},
		{name: "core newer in patch", coreVersion: "0.1.3", pin: "0.1.0", wantNotice: true},
		{name: "core newer in minor", coreVersion: "0.4.0", pin: "0.1.0", wantNotice: true},
		{
			name: "core older than the pin", coreVersion: "0.1.0", pin: "0.2.0",
			wantErr: []Code{CodeCoreTooOld},
		},
		{
			name: "major mismatch", coreVersion: "1.0.0", pin: "0.1.0",
			wantErr: []Code{CodeCoreMajor},
		},
		{
			name: "pin is not semver", coreVersion: "0.1.0", pin: "latest",
			wantErr: []Code{CodeBadVersion},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core := corePackFS(t, "name: core\nversion: "+tt.coreVersion+"\n", nil)
			proj, err := LoadFS(fsWith(map[string]string{
				"pack.yaml": "name: p\nversion: 0.1.0\ncore_version: " + tt.pin + "\n",
			}))
			if err != nil && tt.wantErr == nil {
				t.Fatalf("loading project pack: %v", err)
			}
			if err != nil {
				// A non-semver pin is caught at load; that is still the right code.
				wantCodes(t, err, tt.wantErr...)
				return
			}

			merged, err := Merge(core, proj)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				wantCodes(t, err, tt.wantErr...)
				return
			}
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if got := len(merged.Notices) > 0; got != tt.wantNotice {
				t.Errorf("notices present = %v, want %v (%v)", got, tt.wantNotice, merged.Notices)
			}
		})
	}
}

func TestMergeErrors(t *testing.T) {
	coreRelations := "" +
		"roles:\n" +
		"  - {name: membership}\n" +
		"  - {name: containment}\n" +
		"relations:\n" +
		"  - name: member_of\n" +
		"    domain: [character]\n" +
		"    range: [faction]\n" +
		"    inverse: has_member\n" +
		"    role: membership\n"
	coreTypes := "" +
		"types:\n" +
		"  - {name: character}\n" +
		"  - {name: faction}\n" +
		"groups:\n" +
		"  - {name: agent, includes: [character, faction]}\n" +
		"  - {name: any, includes: [\"*\"]}\n"
	coreFiles := map[string]string{
		"relations.yaml":    coreRelations,
		"entity-types.yaml": coreTypes,
	}

	tests := []struct {
		name  string
		files map[string]string
		want  []Code
	}{
		{
			name: "project relation collides with a core relation",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: member_of, domain: [character], range: [faction]}\n"},
			want: []Code{CodeCollision},
		},
		{
			name: "project relation name collides with a core inverse",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: has_member, domain: [faction], range: [character]}\n"},
			want: []Code{CodeCollision},
		},
		{
			name: "project inverse collides with a core relation name",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: enrols, domain: [faction], range: [character], inverse: member_of}\n"},
			want: []Code{CodeCollision},
		},
		{
			name: "collision differing only in case",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: Member_Of, domain: [character], range: [faction]}\n"},
			want: []Code{CodeCaseCollision},
		},
		{
			name:  "project entity type collides with a core type",
			files: map[string]string{"entity-types.yaml": "types:\n  - {name: character}\n"},
			want:  []Code{CodeCollision},
		},
		{
			name:  "project group collides with a core group",
			files: map[string]string{"entity-types.yaml": "groups:\n  - {name: agent, includes: [character]}\n"},
			want:  []Code{CodeCollision},
		},
		{
			name: "unknown entity type in a domain",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: x, domain: [wyrm], range: [faction]}\n"},
			want: []Code{CodeUnknownType},
		},
		{
			name: "unknown entity type in a range",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: x, domain: [character], range: [wyrm]}\n"},
			want: []Code{CodeUnknownType},
		},
		{
			name: "unknown role",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: x, domain: [character], range: [faction], role: containement}\n"},
			want: []Code{CodeUnknownRole},
		},
		{
			name: "project pack declaring a role",
			files: map[string]string{"relations.yaml": "" +
				"roles:\n" +
				"  - {name: diplomacy}\n" +
				"relations:\n" +
				"  - {name: x, domain: [character], range: [faction], role: diplomacy}\n"},
			want: []Code{CodeRoleInProject},
		},
		{
			name:  "group including an unknown type",
			files: map[string]string{"entity-types.yaml": "groups:\n  - {name: g, includes: [wyrm]}\n"},
			want:  []Code{CodeUnknownType},
		},
		{
			name: "group cycle",
			files: map[string]string{"entity-types.yaml": "" +
				"groups:\n" +
				"  - {name: g, includes: [h]}\n" +
				"  - {name: h, includes: [g]}\n"},
			want: []Code{CodeGroupCycle},
		},
		{
			name: "several faults are all reported",
			files: map[string]string{"relations.yaml": "" +
				"relations:\n" +
				"  - {name: member_of, domain: [character], range: [faction]}\n" +
				"  - {name: y, domain: [wyrm], range: [faction], role: nonesuch}\n"},
			want: []Code{CodeCollision, CodeUnknownRole, CodeUnknownType},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core := corePackFS(t, minimalCorePack, coreFiles)
			proj, err := LoadFS(fsWith(tt.files))
			if err != nil {
				t.Fatalf("loading project pack: %v", err)
			}
			_, err = Merge(core, proj)
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			wantCodes(t, err, tt.want...)
		})
	}
}

// Eras are world content. A core pack that declares one is malformed.
func TestMergeRejectsErasInCore(t *testing.T) {
	core := corePackFS(t, minimalCorePack, map[string]string{
		"eras.yaml": "eras:\n  - {key: dawn, name: Dawn}\n",
	})
	proj, err := LoadFS(fsWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(core, proj); err == nil {
		t.Fatal("expected an error, got none")
	} else {
		wantCodes(t, err, CodeEraInCore)
	}
}

// Merge must not mutate its inputs: the core pack is a package-level singleton,
// so a leak would give the next project the previous project's vocabulary.
func TestMergeDoesNotMutateCore(t *testing.T) {
	core, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	before := len(core.Relations)

	if _, err := LoadProject("testdata/project-ok"); err != nil {
		t.Fatal(err)
	}

	core2, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(core2.Relations); got != before {
		t.Errorf("core relation count changed from %d to %d after a merge", before, got)
	}
	if _, ok := core2.Relation("sworn_to"); ok {
		t.Error("a project relation leaked into the core pack")
	}
}

// Acts are world content, exactly as eras are: the core pack cannot know how
// many acts a story has.
func TestMergeRejectsActsInCore(t *testing.T) {
	core := corePackFS(t, minimalCorePack, map[string]string{
		"acts.yaml": "acts:\n  - {key: act1}\n",
	})
	proj, err := LoadFS(fsWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(core, proj); err == nil {
		t.Fatal("expected an error, got none")
	} else {
		wantCodes(t, err, CodeActInCore)
	}
}

func TestMergeCarriesActs(t *testing.T) {
	core, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	proj, err := LoadFS(fsWith(map[string]string{
		"acts.yaml": "acts:\n  - {key: act1}\n  - {key: act2}\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(core, proj)
	if err != nil {
		t.Fatal(err)
	}
	if ord, ok := merged.ActOrdinal("act2"); !ok || ord != 1 {
		t.Errorf("ActOrdinal(act2) = %d %v, want 1 true", ord, ok)
	}
}
