package resolve

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

var (
	fixtureOnce sync.Once
	fixtureIdx  *index.Index
	fixtureErr  error
)

// fixture returns a resolver over testdata/world-resolve, loaded once. Each
// call gets its own Resolver; the index is never modified.
func fixture(t *testing.T) *Resolver {
	t.Helper()
	fixtureOnce.Do(func() {
		res, err := index.Load(fixtureRepo)
		if err != nil {
			fixtureErr = err
			return
		}
		if res.Index == nil {
			fixtureErr = errors.New(res.Findings.String())
			return
		}
		fixtureIdx = res.Index
	})
	if fixtureErr != nil {
		t.Fatalf("loading the fixture: %v", fixtureErr)
	}
	return New(fixtureIdx)
}

var (
	held   = Worldline{"dec_vale": "held"}
	fell   = Worldline{"dec_vale": "fell"}
	hidden = Worldline{"dec_heir": "hidden"}
)

func mustEntity(t *testing.T, r *Resolver, ctx Context, id string) Entity {
	t.Helper()
	e, err := r.Entity(ctx, id)
	if err != nil {
		t.Fatalf("Entity(%s): %v", id, err)
	}
	return e
}

func hasEdge(e Entity, rel, target string, dir Direction) bool {
	return slices.ContainsFunc(e.Edges, func(ed Edge) bool {
		return ed.Relation == rel && ed.Target == target && ed.Direction == dir
	})
}

func TestBranchVisibility(t *testing.T) {
	r := fixture(t)
	if _, err := r.Entity(Omniscient(nil), "loc_vale_keep"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("loc_vale_keep at baseline: err = %v, want ErrUnknownEntity", err)
	}
	if _, err := r.Entity(Omniscient(fell), "loc_vale_keep"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("loc_vale_keep under fell: err = %v, want ErrUnknownEntity", err)
	}
	if e := mustEntity(t, r, Omniscient(held), "loc_vale_keep"); e.ID != "loc_vale_keep" {
		t.Errorf("got %q", e.ID)
	}

	ids := func(ctx Context) []string {
		sums, err := r.Entities(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range sums {
			out = append(out, s.ID)
		}
		return out
	}
	if slices.Contains(ids(Omniscient(nil)), "loc_vale_keep") {
		t.Error("Entities lists loc_vale_keep at baseline")
	}
	got := ids(Omniscient(held))
	if !slices.Contains(got, "loc_vale_keep") {
		t.Error("Entities omits loc_vale_keep under held")
	}
	if !slices.IsSorted(got) {
		t.Errorf("Entities not sorted by ID: %v", got)
	}
}

func TestStatusDefaultCanonOnly(t *testing.T) {
	r := fixture(t)
	if _, err := r.Entity(Omniscient(nil), "fac_draft"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("draft faction by default: err = %v, want ErrUnknownEntity", err)
	}
	// The canon member's edge to it vanishes with it.
	if e := mustEntity(t, r, Omniscient(nil), "char_draftling"); len(e.Edges) != 0 {
		t.Errorf("draftling shows edges to a draft faction by default: %+v", e.Edges)
	}

	ctx := Omniscient(nil, WithStatuses(world.StatusDraft, world.StatusCanon))
	e := mustEntity(t, r, ctx, "fac_draft")
	if e.Status != string(world.StatusDraft) {
		t.Errorf("status = %q", e.Status)
	}
	if !hasEdge(e, "has_member", "char_draftling", Derived) {
		t.Errorf("draft faction lacks its derived member edge: %+v", e.Edges)
	}
}

func TestDerivedEdges(t *testing.T) {
	r := fixture(t)
	ctx := Omniscient(held)

	// An inverse is shown on the target, under the inverse's name.
	court := mustEntity(t, r, ctx, "fac_court")
	for _, member := range []string{"char_kaelen", "char_miren", "char_conformist"} {
		if !hasEdge(court, "has_member", member, Derived) {
			t.Errorf("fac_court lacks has_member %s: %+v", member, court.Edges)
		}
	}
	kaelen := mustEntity(t, r, ctx, "char_kaelen")
	if !hasEdge(kaelen, "member_of", "fac_court", Out) {
		t.Errorf("kaelen lacks its authored edge: %+v", kaelen.Edges)
	}
	for _, ed := range kaelen.Edges {
		if ed.Relation == "member_of" && ed.Target == "fac_court" {
			if ed.Priority == nil || *ed.Priority != 1 || ed.Role != "membership" {
				t.Errorf("member_of fac_court: priority %v, role %q", ed.Priority, ed.Role)
			}
		}
	}

	// A symmetric edge reads the same from both ends.
	vesk := mustEntity(t, r, ctx, "char_vesk")
	conf := mustEntity(t, r, ctx, "char_conformist")
	if !hasEdge(vesk, "sibling_of", "char_conformist", Out) {
		t.Errorf("vesk lacks sibling_of: %+v", vesk.Edges)
	}
	if !hasEdge(conf, "sibling_of", "char_vesk", Derived) {
		t.Errorf("conformist lacks the mirrored sibling_of: %+v", conf.Edges)
	}

	// A one-way relation is visible from its target as In, under its own name.
	heir := mustEntity(t, r, ctx, "char_heir")
	if !hasEdge(heir, "mentions", "char_conformist", In) {
		t.Errorf("heir lacks the inbound mentions: %+v", heir.Edges)
	}
	for _, ed := range heir.Edges {
		if ed.Relation == "mentions" && ed.Role != "soft_link" {
			t.Errorf("mentions role = %q", ed.Role)
		}
	}
}

func TestEdgesToExcludedTargetsVanish(t *testing.T) {
	r := fixture(t)

	horn := mustEntity(t, r, Omniscient(nil), "obj_horn")
	if len(horn.Edges) != 0 {
		t.Errorf("baseline: horn shows edges into an absent keep: %+v", horn.Edges)
	}
	vale := mustEntity(t, r, Omniscient(nil), "loc_vale")
	if hasEdge(vale, "contains", "loc_vale_keep", Derived) {
		t.Error("baseline: the vale contains an absent keep")
	}

	horn = mustEntity(t, r, Omniscient(held), "obj_horn")
	if !hasEdge(horn, "located_in", "loc_vale_keep", Out) {
		t.Errorf("held: horn lacks located_in: %+v", horn.Edges)
	}
	keep := mustEntity(t, r, Omniscient(held), "loc_vale_keep")
	if !hasEdge(keep, "contains", "obj_horn", Derived) {
		t.Errorf("held: keep lacks the derived contains: %+v", keep.Edges)
	}
}

func TestEntityEdgesSorted(t *testing.T) {
	r := fixture(t)
	court := mustEntity(t, r, Omniscient(nil), "fac_court")
	if !slices.IsSortedFunc(court.Edges, compareEdges) {
		t.Errorf("edges not sorted: %+v", court.Edges)
	}
}

func TestUnknownEntity(t *testing.T) {
	r := fixture(t)
	if _, err := r.Entity(Omniscient(nil), "char_nobody"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("err = %v, want ErrUnknownEntity", err)
	}
	// A statement is not an entity.
	if _, err := r.Entity(Omniscient(nil), "stmt_heir_lives"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("statement as entity: err = %v, want ErrUnknownEntity", err)
	}
	if _, err := r.Entity(Context{}, "char_heir"); !errors.Is(err, ErrNoContext) {
		t.Errorf("zero context: err = %v, want ErrNoContext", err)
	}
	if _, err := r.Entities(Context{}); !errors.Is(err, ErrNoContext) {
		t.Errorf("Entities with zero context: err = %v, want ErrNoContext", err)
	}
}
