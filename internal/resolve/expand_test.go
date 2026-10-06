package resolve

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gmreyer/lorekeep/internal/index"
)

func mustExpand(t *testing.T, r *Resolver, ctx Context, id string, depth int, rels ...string) Graph {
	t.Helper()
	g, err := r.Expand(ctx, id, depth, rels)
	if err != nil {
		t.Fatalf("Expand(%s, %d, %v): %v", id, depth, rels, err)
	}
	return g
}

func nodeIDs(g Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestExpandDepth(t *testing.T) {
	r := fixture(t)
	ctx := Omniscient(nil)

	g := mustExpand(t, r, ctx, "char_kaelen", 1)
	if g.Root != "char_kaelen" {
		t.Errorf("Root = %q, want char_kaelen", g.Root)
	}
	if diff := cmp.Diff([]string{"char_kaelen", "fac_court", "fac_order"}, nodeIDs(g)); diff != "" {
		t.Errorf("depth 1 nodes (-want +got):\n%s", diff)
	}
	wantEdges := []GraphEdge{
		{From: "char_kaelen", Relation: "member_of", To: "fac_court", Role: "membership"},
		{From: "char_kaelen", Relation: "member_of", To: "fac_order", Role: "membership"},
	}
	if diff := cmp.Diff(wantEdges, g.Edges); diff != "" {
		t.Errorf("depth 1 edges (-want +got):\n%s", diff)
	}

	g = mustExpand(t, r, ctx, "char_kaelen", 2)
	want := []string{"char_conformist", "char_kaelen", "char_miren", "fac_court", "fac_order"}
	if diff := cmp.Diff(want, nodeIDs(g)); diff != "" {
		t.Errorf("depth 2 nodes (-want +got):\n%s", diff)
	}
	wantEdges = []GraphEdge{
		{From: "char_conformist", Relation: "member_of", To: "fac_court", Role: "membership"},
		{From: "char_kaelen", Relation: "member_of", To: "fac_court", Role: "membership"},
		{From: "char_kaelen", Relation: "member_of", To: "fac_order", Role: "membership"},
		{From: "char_miren", Relation: "member_of", To: "fac_court", Role: "membership"},
	}
	if diff := cmp.Diff(wantEdges, g.Edges); diff != "" {
		t.Errorf("depth 2 edges (-want +got):\n%s", diff)
	}

	// Depth 3 reaches the conformist's neighbours, through both of her
	// edges: one she authors and one authored at her.
	g = mustExpand(t, r, ctx, "char_kaelen", 3)
	want = []string{"char_conformist", "char_heir", "char_kaelen", "char_miren", "char_vesk", "fac_court", "fac_order"}
	if diff := cmp.Diff(want, nodeIDs(g)); diff != "" {
		t.Errorf("depth 3 nodes (-want +got):\n%s", diff)
	}
	if !slices.Contains(g.Edges, GraphEdge{From: "char_vesk", Relation: "sibling_of", To: "char_conformist", Role: r.relations["sibling_of"].Role}) {
		t.Errorf("depth 3: missing the authored sibling_of edge, got %v", g.Edges)
	}
}

func TestExpandFiltersRelation(t *testing.T) {
	r := fixture(t)
	ctx := Omniscient(nil)

	g := mustExpand(t, r, ctx, "char_conformist", 1, "mentions")
	if diff := cmp.Diff([]string{"char_conformist", "char_heir"}, nodeIDs(g)); diff != "" {
		t.Errorf("mentions nodes (-want +got):\n%s", diff)
	}
	for _, e := range g.Edges {
		if e.Relation != "mentions" {
			t.Errorf("unfiltered edge %v", e)
		}
	}

	// The filter holds at every depth: following only member_of, Kaelen
	// never reaches Vesk or the heir.
	g = mustExpand(t, r, ctx, "char_kaelen", 3, "member_of")
	want := []string{"char_conformist", "char_kaelen", "char_miren", "fac_court", "fac_order"}
	if diff := cmp.Diff(want, nodeIDs(g)); diff != "" {
		t.Errorf("member_of depth 3 nodes (-want +got):\n%s", diff)
	}

	// An inverse name selects its forward relation; edges stay authored.
	g = mustExpand(t, r, ctx, "fac_court", 1, "has_member")
	want = []string{"char_conformist", "char_kaelen", "char_miren", "fac_court"}
	if diff := cmp.Diff(want, nodeIDs(g)); diff != "" {
		t.Errorf("has_member nodes (-want +got):\n%s", diff)
	}
	for _, e := range g.Edges {
		if e.Relation != "member_of" || e.To != "fac_court" {
			t.Errorf("edge not in authored direction: %v", e)
		}
	}
}

func TestExpandRespectsBranch(t *testing.T) {
	r := fixture(t)

	g := mustExpand(t, r, Omniscient(held), "loc_vale", 2)
	if diff := cmp.Diff([]string{"loc_vale", "loc_vale_keep", "obj_horn"}, nodeIDs(g)); diff != "" {
		t.Errorf("held nodes (-want +got):\n%s", diff)
	}

	g = mustExpand(t, r, Omniscient(fell), "loc_vale", 3)
	if diff := cmp.Diff([]string{"loc_vale"}, nodeIDs(g)); diff != "" {
		t.Errorf("fell nodes (-want +got):\n%s", diff)
	}
	if len(g.Edges) != 0 {
		t.Errorf("fell edges = %v, want none", g.Edges)
	}

	// The horn is unconditional, but its only edge is to the keep.
	g = mustExpand(t, r, Omniscient(fell), "obj_horn", 3)
	if diff := cmp.Diff([]string{"obj_horn"}, nodeIDs(g)); diff != "" {
		t.Errorf("horn under fell (-want +got):\n%s", diff)
	}

	// A conditional membership exists only in its branch.
	g = mustExpand(t, r, Omniscient(nil), "fac_veil", 1)
	if diff := cmp.Diff([]string{"fac_veil"}, nodeIDs(g)); diff != "" {
		t.Errorf("veil at baseline (-want +got):\n%s", diff)
	}
	g = mustExpand(t, r, Omniscient(hidden), "fac_veil", 1)
	if diff := cmp.Diff([]string{"char_ward", "fac_veil"}, nodeIDs(g)); diff != "" {
		t.Errorf("veil under hidden (-want +got):\n%s", diff)
	}

	// A draft entity is absent from a canon read, and its edges with it.
	g = mustExpand(t, r, Omniscient(nil), "char_draftling", 3)
	if diff := cmp.Diff([]string{"char_draftling"}, nodeIDs(g)); diff != "" {
		t.Errorf("draftling (-want +got):\n%s", diff)
	}

	if _, err := r.Expand(Omniscient(fell), "loc_vale_keep", 1, nil); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("absent root: err = %v, want ErrUnknownEntity", err)
	}
	if _, err := r.Expand(Omniscient(nil), "no_such_entity", 1, nil); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("unknown root: err = %v, want ErrUnknownEntity", err)
	}
	if _, err := r.Expand(Context{}, "loc_vale", 1, nil); !errors.Is(err, ErrNoContext) {
		t.Errorf("zero context: err = %v, want ErrNoContext", err)
	}
}

func TestExpandUnknownRelation(t *testing.T) {
	r := fixture(t)
	for _, rels := range [][]string{{"no_such_relation"}, {"member_of", "no_such_relation"}, {""}} {
		if _, err := r.Expand(Omniscient(nil), "char_kaelen", 1, rels); !errors.Is(err, ErrUnknownRelation) {
			t.Errorf("Expand(%q): err = %v, want ErrUnknownRelation", rels, err)
		}
	}
}

// chainIndex is a line of five characters, each a member of the next, so a
// walk longer than three hops has somewhere to go.
func chainIndex() *index.Index {
	idx := &index.Index{
		Format: index.Format,
		Vocabulary: index.Vocabulary{
			Groups:    []index.GroupInfo{{Name: "agent", Types: []string{"character"}}},
			Relations: []index.Relation{{Name: "member_of", Inverse: "has_member", Role: "membership"}},
		},
	}
	ids := []string{"c1", "c2", "c3", "c4", "c5"}
	for i, id := range ids {
		e := index.Entity{ID: id, Type: "character", Status: "canon", Visibility: "internal"}
		if i+1 < len(ids) {
			e.Edges = []index.Edge{{Relation: "member_of", Target: ids[i+1]}}
		}
		idx.Entities = append(idx.Entities, e)
	}
	return idx
}

func TestExpandClampsDepth(t *testing.T) {
	r := New(chainIndex())
	ctx := Omniscient(nil)

	if diff := cmp.Diff([]string{"c1", "c2", "c3", "c4"}, nodeIDs(mustExpand(t, r, ctx, "c1", 9))); diff != "" {
		t.Errorf("depth 9 (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(mustExpand(t, r, ctx, "c1", 3), mustExpand(t, r, ctx, "c1", 9)); diff != "" {
		t.Errorf("depth 9 differs from depth 3 (-3 +9):\n%s", diff)
	}
	for _, d := range []int{0, -4} {
		if diff := cmp.Diff([]string{"c1", "c2"}, nodeIDs(mustExpand(t, r, ctx, "c1", d))); diff != "" {
			t.Errorf("depth %d (-want +got):\n%s", d, diff)
		}
	}

	// Followed from either end: from the middle, depth 1 reaches both sides.
	g := mustExpand(t, r, ctx, "c3", 1)
	if diff := cmp.Diff([]string{"c2", "c3", "c4"}, nodeIDs(g)); diff != "" {
		t.Errorf("from c3 (-want +got):\n%s", diff)
	}
	wantEdges := []GraphEdge{
		{From: "c2", Relation: "member_of", To: "c3", Role: "membership"},
		{From: "c3", Relation: "member_of", To: "c4", Role: "membership"},
	}
	if diff := cmp.Diff(wantEdges, g.Edges); diff != "" {
		t.Errorf("from c3 edges (-want +got):\n%s", diff)
	}
}
