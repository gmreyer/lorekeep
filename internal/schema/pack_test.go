package schema

import (
	"slices"
	"testing"
)

// Domain and range are cross products over their lists, and a group in either
// position expands to its concrete types.
func TestAllowsDomainAndRange(t *testing.T) {
	p := mustLoadFixture(t)

	tests := []struct {
		rel        string
		typ        string
		wantDomain bool
		wantRange  bool
	}{
		// member_of: [character, faction] -> [faction]
		{rel: "member_of", typ: "character", wantDomain: true},
		{rel: "member_of", typ: "faction", wantDomain: true, wantRange: true},
		{rel: "member_of", typ: "location"},

		// participated_in: [agent] -> [event]; the group expands.
		{rel: "participated_in", typ: "character", wantDomain: true},
		{rel: "participated_in", typ: "faction", wantDomain: true},
		{rel: "participated_in", typ: "event", wantRange: true},
		{rel: "participated_in", typ: "object"},

		// speaks: [agent] -> [language]; a core group over a project type.
		{rel: "speaks", typ: "character", wantDomain: true},
		{rel: "speaks", typ: "language", wantRange: true},

		// practises: [agent] -> [cultural]; a project group mixing core and
		// project types.
		{rel: "practises", typ: "concept", wantRange: true},
		{rel: "practises", typ: "language", wantRange: true},
		{rel: "practises", typ: "object"},

		// mentions: any -> any, and the wildcard picks up the project's type.
		{rel: "mentions", typ: "character", wantDomain: true, wantRange: true},
		{rel: "mentions", typ: "decision", wantDomain: true, wantRange: true},
		{rel: "mentions", typ: "language", wantDomain: true, wantRange: true},

		// sworn_to: [character] -> [character, faction]
		{rel: "sworn_to", typ: "character", wantDomain: true, wantRange: true},
		{rel: "sworn_to", typ: "faction", wantRange: true},
	}

	for _, tt := range tests {
		if got := p.AllowsDomain(tt.rel, tt.typ); got != tt.wantDomain {
			t.Errorf("AllowsDomain(%q, %q) = %v, want %v", tt.rel, tt.typ, got, tt.wantDomain)
		}
		if got := p.AllowsRange(tt.rel, tt.typ); got != tt.wantRange {
			t.Errorf("AllowsRange(%q, %q) = %v, want %v", tt.rel, tt.typ, got, tt.wantRange)
		}
	}
}

func TestAllowsUnknownRelationOrType(t *testing.T) {
	p := mustLoadFixture(t)
	if p.AllowsDomain("nonesuch", "character") {
		t.Error("AllowsDomain accepted an undeclared relation")
	}
	if p.AllowsRange("member_of", "wyrm") {
		t.Error("AllowsRange accepted an undeclared entity type")
	}
	if p.HasType("wyrm") {
		t.Error("HasType accepted an undeclared entity type")
	}
	// A group is not an entity type: nothing is of type "agent".
	if p.HasType("agent") {
		t.Error("HasType treated the group agent as an entity type")
	}
}

// An inverse is a derived name, never an authored one. Callers need to
// recognise one to reject it on an entity and to traverse backwards.
func TestIsInverseName(t *testing.T) {
	p := mustLoadFixture(t)

	tests := []struct {
		name        string
		wantForward string
		wantOK      bool
	}{
		{name: "has_member", wantForward: "member_of", wantOK: true},
		{name: "contains", wantForward: "located_in", wantOK: true},
		{name: "was_site_of", wantForward: "occurred_at", wantOK: true},
		{name: "has_sworn", wantForward: "sworn_to", wantOK: true},
		{name: "member_of"},  // a forward name is not an inverse
		{name: "sibling_of"}, // symmetric: its own inverse, but not a derived name
		{name: "mentions"},   // no inverse declared
		{name: "nonesuch"},
	}

	for _, tt := range tests {
		got, ok := p.IsInverseName(tt.name)
		if ok != tt.wantOK || got != tt.wantForward {
			t.Errorf("IsInverseName(%q) = (%q, %v), want (%q, %v)",
				tt.name, got, ok, tt.wantForward, tt.wantOK)
		}
	}
}

// Accessors return a stable order, so callers that render or hash a pack do
// not depend on map iteration.
func TestDeterministicOrder(t *testing.T) {
	first := mustLoadFixture(t)
	second := mustLoadFixture(t)

	names := func(p *Pack) []string {
		var out []string
		for _, r := range p.Relations {
			out = append(out, r.Name)
		}
		return out
	}
	if !slices.Equal(names(first), names(second)) {
		t.Error("relation order is not stable across loads")
	}

	// Core relations come first, then the project's; each keeps file order.
	got := names(first)
	coreIdx := slices.Index(got, "mentions")
	projIdx := slices.Index(got, "allied_with")
	if coreIdx < 0 || projIdx < 0 || coreIdx > projIdx {
		t.Errorf("core relations should precede project relations: %v", got)
	}
}

func TestRelationReturnsACopy(t *testing.T) {
	p := mustLoadFixture(t)
	rel, ok := p.Relation("member_of")
	if !ok {
		t.Fatal("member_of missing")
	}
	rel.Role = "tampered"

	again, _ := p.Relation("member_of")
	if again.Role != "membership" {
		t.Errorf("mutating a returned relation changed the pack: role = %q", again.Role)
	}
}
