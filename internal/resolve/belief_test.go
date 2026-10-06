package resolve

import (
	"errors"
	"testing"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

// beliefOn returns the agent's resolved belief on stmt under ctx.
func beliefOn(t *testing.T, r *Resolver, ctx Context, agent, stmt string) (Belief, bool) {
	t.Helper()
	bs, err := r.Beliefs(ctx, agent)
	if err != nil {
		t.Fatalf("Beliefs(%s): %v", agent, err)
	}
	for _, b := range bs {
		if b.Statement == stmt {
			return b, true
		}
	}
	return Belief{}, false
}

func wantBelief(t *testing.T, got Belief, ok bool, held string, src Source, via string) {
	t.Helper()
	if !ok {
		t.Fatalf("no belief, want %s %s via %q", held, src, via)
	}
	if got.Held != held || got.Source != src || got.Via != via {
		t.Errorf("belief = %s %s via %q, want %s %s via %q", got.Held, got.Source, got.Via, held, src, via)
	}
}

func TestExplicitBeatsInherited(t *testing.T) {
	r := fixture(t)
	b, ok := beliefOn(t, r, Scoped(nil, "char_miren"), "char_miren", "stmt_miren_oath")
	wantBelief(t, b, ok, "false", Explicit, "")
}

func TestPriorityResolvesDualMembership(t *testing.T) {
	r := fixture(t)
	b, ok := beliefOn(t, r, Scoped(nil, "char_kaelen"), "char_kaelen", "stmt_heir_lives")
	wantBelief(t, b, ok, "true", Inherited, "fac_court")
	if b.Subject != "char_heir" || b.Relation != "heir_of" || b.Object != "fac_court" {
		t.Errorf("triple = %s %s %s", b.Subject, b.Relation, b.Object)
	}
}

func TestInheritanceSkipsExcludedMembership(t *testing.T) {
	r := fixture(t)

	// Ward joins the Veil only if the heir stays hidden.
	if b, ok := beliefOn(t, r, Scoped(nil, "char_ward"), "char_ward", "stmt_veil_secret"); ok {
		t.Errorf("baseline: Ward inherited through a membership that does not hold: %+v", b)
	}
	b, ok := beliefOn(t, r, Scoped(hidden, "char_ward"), "char_ward", "stmt_veil_secret")
	wantBelief(t, b, ok, "true", Inherited, "fac_veil")
	if b, ok := beliefOn(t, r, Scoped(Worldline{"dec_heir": "named"}, "char_ward"), "char_ward", "stmt_veil_secret"); ok {
		t.Errorf("named: Ward inherited through a membership that does not hold: %+v", b)
	}

	// A draft faction passes nothing unless drafts are read.
	if b, ok := beliefOn(t, r, Scoped(nil, "char_draftling"), "char_draftling", "stmt_heir_lives"); ok {
		t.Errorf("draftling inherited from a draft faction by default: %+v", b)
	}
	drafts := WithStatuses(world.StatusDraft, world.StatusCanon)
	b, ok = beliefOn(t, r, Scoped(nil, "char_draftling", drafts), "char_draftling", "stmt_heir_lives")
	wantBelief(t, b, ok, "true", Inherited, "fac_draft")
}

func TestCommonKnowledge(t *testing.T) {
	r := fixture(t)
	for _, agent := range []string{"char_kaelen", "char_ward", "fac_order"} {
		b, ok := beliefOn(t, r, Scoped(nil, agent), agent, "stmt_heir_common")
		wantBelief(t, b, ok, "true", Common, "")
	}
	// Not common: an agent with no source is ignorant, not told the truth.
	if b, ok := beliefOn(t, r, Scoped(nil, "char_ward"), "char_ward", "stmt_heir_lives"); ok {
		t.Errorf("Ward knows stmt_heir_lives with no source: %+v", b)
	}
}

func TestBeliefOnExcludedStatementDropped(t *testing.T) {
	r := fixture(t)
	for _, wl := range []Worldline{nil, held} {
		if b, ok := beliefOn(t, r, Scoped(wl, "char_vesk"), "char_vesk", "stmt_heir_sworn_fell"); ok {
			t.Errorf("worldline %v: belief on an absent statement shown: %+v", wl, b)
		}
	}
	b, ok := beliefOn(t, r, Scoped(fell, "char_vesk"), "char_vesk", "stmt_heir_sworn_fell")
	wantBelief(t, b, ok, "true", Explicit, "")
}

func TestFactionKnower(t *testing.T) {
	r := fixture(t)
	ctx := Scoped(nil, "fac_order")
	b, ok := beliefOn(t, r, ctx, "fac_order", "stmt_heir_lives")
	wantBelief(t, b, ok, "false", Explicit, "")
	if b, ok := beliefOn(t, r, ctx, "fac_order", "stmt_miren_oath"); ok {
		t.Errorf("fac_order holds the court's belief: %+v", b)
	}
}

func TestBeliefsOutOfScope(t *testing.T) {
	r := fixture(t)
	if _, err := r.Beliefs(Scoped(nil, "char_kaelen"), "char_miren"); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("Kaelen reading Miren's mind: err = %v, want ErrOutOfScope", err)
	}
	if _, err := r.Beliefs(Omniscient(nil), "char_miren"); err != nil {
		t.Errorf("omniscient read of Miren: %v", err)
	}
	if _, err := r.Beliefs(Omniscient(nil), "loc_vale"); !errors.Is(err, ErrNotAgent) {
		t.Errorf("a location's beliefs: err = %v, want ErrNotAgent", err)
	}
	if _, err := r.Beliefs(Context{}, "char_miren"); !errors.Is(err, ErrNoContext) {
		t.Errorf("zero context: err = %v, want ErrNoContext", err)
	}
}

func TestOmniscientShowsCanonTruth(t *testing.T) {
	r := fixture(t)
	heir := mustEntity(t, r, Omniscient(nil), "char_heir")
	got := map[string]Belief{}
	for _, b := range heir.Statements {
		got[b.Statement] = b
		if b.Source != Canon {
			t.Errorf("%s: source %s, want canon", b.Statement, b.Source)
		}
	}
	for _, id := range []string{"stmt_heir_lives", "stmt_heir_common", "stmt_veil_secret"} {
		if got[id].Held != "true" {
			t.Errorf("%s: held %q, want canon truth true", id, got[id].Held)
		}
	}
	if _, ok := got["stmt_heir_sworn_fell"]; ok {
		t.Error("a statement valid only in fell shown at baseline")
	}
}

// A scoped read of an entity shows the knower's beliefs about it, and leaves
// out what the knower is ignorant of.
func TestScopedEntityStatements(t *testing.T) {
	r := fixture(t)
	heir := mustEntity(t, r, Scoped(nil, "char_kaelen"), "char_heir")
	got := map[string]Belief{}
	for _, b := range heir.Statements {
		got[b.Statement] = b
	}
	if _, ok := got["stmt_veil_secret"]; ok {
		t.Error("Kaelen sees a statement he is ignorant of")
	}
	if b := got["stmt_heir_lives"]; b.Source != Inherited || b.Via != "fac_court" {
		t.Errorf("stmt_heir_lives = %+v", b)
	}
}

// The most specific of an agent's own beliefs wins: one conditioned on the
// worldline beats an unconditional one, and ties go to the first authored.
func TestMostSpecificOwnBeliefWins(t *testing.T) {
	idx := tinyIndex()
	idx.Statements = []index.Statement{{ID: "stmt_x", Subject: "char_a", Relation: "member_of",
		Object: "fac_b", Truth: "true", Status: "canon", Visibility: "internal"}}
	idx.Entities[1].Beliefs = []index.Belief{
		{Statement: "stmt_x", Value: true},
		{Statement: "stmt_x", Value: false, ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "fell"}}},
		{Statement: "stmt_x", Value: true, ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "fell"}}},
	}
	r := New(idx)
	b, ok := beliefOn(t, r, Scoped(nil, "char_a"), "char_a", "stmt_x")
	wantBelief(t, b, ok, "true", Explicit, "")
	b, ok = beliefOn(t, r, Scoped(fell, "char_a"), "char_a", "stmt_x")
	wantBelief(t, b, ok, "false", Explicit, "")
}

// Unnumbered memberships follow numbered ones, in authored order.
func TestUnnumberedMembershipsLast(t *testing.T) {
	two := 2
	idx := tinyIndex()
	idx.Entities = append(idx.Entities,
		index.Entity{ID: "fac_c", Type: "faction", Status: "canon", Visibility: "internal",
			Beliefs: []index.Belief{{Statement: "stmt_x", Value: false}}},
		index.Entity{ID: "fac_d", Type: "faction", Status: "canon", Visibility: "internal",
			Beliefs: []index.Belief{{Statement: "stmt_x", Value: true}}},
	)
	idx.Entities[1].Edges = []index.Edge{
		{Relation: "member_of", Target: "fac_c"},
		{Relation: "member_of", Target: "fac_d", Priority: &two},
	}
	idx.Statements = []index.Statement{{ID: "stmt_x", Subject: "char_a", Relation: "member_of",
		Object: "fac_b", Truth: "true", Status: "canon", Visibility: "internal"}}
	r := New(idx)
	b, ok := beliefOn(t, r, Scoped(nil, "char_a"), "char_a", "stmt_x")
	wantBelief(t, b, ok, "true", Inherited, "fac_d")
}

// A statement about an entity absent under the context is absent too, and a
// belief never names an absent source: either would tell the reader that the
// hidden entity exists somewhere in the story.
func TestNothingNamesAnAbsentEntity(t *testing.T) {
	idx := tinyIndex()
	idx.Statements = []index.Statement{{ID: "stmt_keep", Subject: "char_a", Relation: "member_of",
		Object: "loc_keep", Truth: "true", Common: true, Status: "canon", Visibility: "internal"}}
	idx.Entities[1].Beliefs = []index.Belief{{Statement: "stmt_x", Value: true, AcquiredFrom: "loc_keep"}}
	idx.Statements = append(idx.Statements, index.Statement{ID: "stmt_x", Subject: "char_a",
		Relation: "member_of", Object: "fac_b", Truth: "true", Status: "canon", Visibility: "internal"})
	r := New(idx)

	if b, ok := beliefOn(t, r, Scoped(nil, "char_a"), "char_a", "stmt_keep"); ok {
		t.Errorf("baseline: a statement about the absent keep is shown: %+v", b)
	}
	if b, ok := beliefOn(t, r, Scoped(held, "char_a"), "char_a", "stmt_keep"); !ok || b.Source != Common {
		t.Errorf("held: the statement about the keep is missing: %+v", b)
	}
	if e := mustEntity(t, r, Omniscient(nil), "char_a"); len(e.Statements) != 1 {
		t.Errorf("baseline: char_a statements = %+v, want stmt_x only", e.Statements)
	}

	b, ok := beliefOn(t, r, Scoped(nil, "char_a"), "char_a", "stmt_x")
	if !ok || b.AcquiredFrom != "" {
		t.Errorf("baseline: belief names an absent source: %+v", b)
	}
	b, _ = beliefOn(t, r, Scoped(held, "char_a"), "char_a", "stmt_x")
	if b.AcquiredFrom != "loc_keep" {
		t.Errorf("held: acquired_from lost: %+v", b)
	}
}

// A faction is a group others are members of; it reads its own beliefs and
// common knowledge, and inherits from nothing, even when it is itself a member
// of another faction.
func TestFactionKnowerWithMembershipDoesNotInherit(t *testing.T) {
	idx := tinyIndex()
	idx.Statements = []index.Statement{{ID: "stmt_x", Subject: "char_a", Relation: "member_of",
		Object: "fac_b", Truth: "true", Status: "canon", Visibility: "internal"}}
	idx.Entities = append(idx.Entities, index.Entity{ID: "fac_c", Type: "faction", Status: "canon",
		Visibility: "internal", Beliefs: []index.Belief{{Statement: "stmt_x", Value: false}}})
	idx.Entities[2].Edges = []index.Edge{{Relation: "member_of", Target: "fac_c"}}
	idx.Entities[1].Edges = []index.Edge{{Relation: "member_of", Target: "fac_c"}}
	r := New(idx)
	if b, ok := beliefOn(t, r, Scoped(nil, "fac_b"), "fac_b", "stmt_x"); ok {
		t.Errorf("a faction inherited from its parent faction: %+v", b)
	}
	b, ok := beliefOn(t, r, Scoped(nil, "char_a"), "char_a", "stmt_x")
	wantBelief(t, b, ok, "false", Inherited, "fac_c")
}

// An inherited belief names the faction it came from; where the faction got
// it from is the faction's business, not the member's.
func TestInheritedBeliefDropsAcquiredFrom(t *testing.T) {
	idx := tinyIndex()
	idx.Statements = []index.Statement{{ID: "stmt_x", Subject: "char_a", Relation: "member_of",
		Object: "fac_b", Truth: "true", Status: "canon", Visibility: "internal"}}
	idx.Entities[2].Beliefs = []index.Belief{{Statement: "stmt_x", Value: true, Confidence: "high", AcquiredFrom: "char_a"}}
	idx.Entities[1].Edges = []index.Edge{{Relation: "member_of", Target: "fac_b"}}
	b, ok := beliefOn(t, New(idx), Scoped(nil, "char_a"), "char_a", "stmt_x")
	wantBelief(t, b, ok, "true", Inherited, "fac_b")
	if b.AcquiredFrom != "" {
		t.Errorf("inherited belief carries the faction's source %q", b.AcquiredFrom)
	}
	if b.Confidence != "high" {
		t.Errorf("confidence = %q, want the faction's", b.Confidence)
	}
}

// Another agent's mind is out of scope whether or not that agent exists.
func TestBeliefsScopeBeforePresence(t *testing.T) {
	r := fixture(t)
	if _, err := r.Beliefs(Scoped(nil, "char_kaelen"), "char_nobody"); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("err = %v, want ErrOutOfScope", err)
	}
}
