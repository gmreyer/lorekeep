package resolve

import (
	"errors"
	"testing"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/google/go-cmp/cmp"
)

func TestDiffHeldVsFell(t *testing.T) {
	r := fixture(t)
	got, err := r.Diff(Omniscient(held), Omniscient(fell))
	if err != nil {
		t.Fatal(err)
	}
	want := Diff{
		OnlyA: []Fact{
			{Kind: "edge", Subject: "loc_vale_keep", Relation: "located_in", Object: "loc_vale"},
			{Kind: "edge", Subject: "obj_horn", Relation: "located_in", Object: "loc_vale_keep"},
			{Kind: "entity", Subject: "loc_vale_keep"},
		},
		OnlyB: []Fact{
			{Kind: "belief", Subject: "char_vesk", Object: "stmt_heir_sworn_fell", Value: "true"},
			{Kind: "statement", Subject: "stmt_heir_sworn_fell", Value: "true"},
		},
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("Diff(held, fell) mismatch (-want +got):\n%s", d)
	}
}

func TestDiffSameWorldlineEmpty(t *testing.T) {
	r := fixture(t)
	for name, wl := range map[string]Worldline{"baseline": nil, "held": held, "fell": fell, "hidden": hidden} {
		got, err := r.Diff(Omniscient(wl), Omniscient(wl))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got.OnlyA) != 0 || len(got.OnlyB) != 0 {
			t.Errorf("%s vs itself: got %+v, want empty", name, got)
		}
	}
}

func TestDiffRequiresOmniscient(t *testing.T) {
	r := fixture(t)
	scoped := Scoped(held, "char_kaelen")
	if _, err := r.Diff(scoped, Omniscient(fell)); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("scoped a: err = %v, want ErrOutOfScope", err)
	}
	if _, err := r.Diff(Omniscient(held), scoped); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("scoped b: err = %v, want ErrOutOfScope", err)
	}
	if _, err := r.Diff(Context{}, Omniscient(fell)); !errors.Is(err, ErrNoContext) {
		t.Errorf("zero a: err = %v, want ErrNoContext", err)
	}
	if _, err := r.Diff(Omniscient(held), Omniscient(Worldline{"dec_vale": "lost"})); !errors.Is(err, ErrUnknownOutcome) {
		t.Errorf("bad outcome b: err = %v, want ErrUnknownOutcome", err)
	}
}

// A priority that differs between branches changes who inherits what, and a
// lie told in one branch only is a difference: both are facts.
func TestDiffPriorityAndAssertions(t *testing.T) {
	one, two := 1, 2
	idx := tinyIndex()
	idx.Statements = []index.Statement{{ID: "stmt_x", Subject: "char_a", Relation: "member_of",
		Object: "fac_b", Truth: "true", Status: "canon", Visibility: "internal"}}
	idx.Entities[1].Edges = []index.Edge{
		{Relation: "member_of", Target: "fac_b", Priority: &one, ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "held"}}},
		{Relation: "member_of", Target: "fac_b", Priority: &two, ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "fell"}}},
	}
	idx.Entities[1].Assertions = []index.Assertion{
		{Statement: "stmt_x", Value: false, ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "fell"}}},
	}
	got, err := New(idx).Diff(Omniscient(held), Omniscient(fell))
	if err != nil {
		t.Fatal(err)
	}
	want := Diff{
		OnlyA: []Fact{
			{Kind: FactEdge, Subject: "char_a", Relation: "member_of", Object: "fac_b", Value: "1"},
			{Kind: FactEntity, Subject: "loc_keep"},
		},
		OnlyB: []Fact{
			{Kind: FactAssertion, Subject: "char_a", Object: "stmt_x", Value: "false"},
			{Kind: FactEdge, Subject: "char_a", Relation: "member_of", Object: "fac_b", Value: "2"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Diff (-want +got):\n%s", diff)
	}
}
