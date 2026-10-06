package resolve

import (
	"errors"
	"testing"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

// tinyIndex is a hand-built world small enough to read at a glance: one
// decision, one character, one faction, and a location that exists only when
// the keep held.
func tinyIndex() *index.Index {
	return &index.Index{
		Format: index.Format,
		Vocabulary: index.Vocabulary{
			Groups: []index.GroupInfo{{Name: "agent", Types: []string{"character", "faction"}}},
			Relations: []index.Relation{
				{Name: "member_of", Domain: []string{"character", "faction"}, Range: []string{"faction"},
					Inverse: "has_member", Role: "membership"},
			},
		},
		Entities: []index.Entity{
			{ID: "dec_vale", Type: "decision", Status: "canon", Visibility: "internal", Outcomes: []string{"held", "fell"}},
			{ID: "char_a", Type: "character", Status: "canon", Visibility: "internal"},
			{ID: "fac_b", Type: "faction", Status: "canon", Visibility: "internal"},
			{ID: "loc_keep", Type: "location", Status: "canon", Visibility: "internal",
				ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "held"}}},
		},
	}
}

func TestHolds(t *testing.T) {
	held := index.Condition{Decision: "dec_vale", Outcome: "held"}
	named := index.Condition{Decision: "dec_heir", Outcome: "named"}
	tests := []struct {
		name  string
		conds []index.Condition
		wl    Worldline
		want  bool
	}{
		{"no conditions", nil, nil, true},
		{"no conditions, any worldline", nil, Worldline{"dec_vale": "fell"}, true},
		{"matching outcome", []index.Condition{held}, Worldline{"dec_vale": "held"}, true},
		{"other outcome", []index.Condition{held}, Worldline{"dec_vale": "fell"}, false},
		{"decision unassigned", []index.Condition{held}, nil, false},
		{"both hold", []index.Condition{held, named}, Worldline{"dec_vale": "held", "dec_heir": "named"}, true},
		{"one of two fails", []index.Condition{held, named}, Worldline{"dec_vale": "held", "dec_heir": "hidden"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := holds(tt.conds, tt.wl); got != tt.want {
				t.Errorf("holds(%v, %v) = %t, want %t", tt.conds, tt.wl, got, tt.want)
			}
		})
	}
}

func TestZeroContextRefused(t *testing.T) {
	r := New(tinyIndex())
	if err := r.check(Context{}); !errors.Is(err, ErrNoContext) {
		t.Errorf("check(Context{}) = %v, want ErrNoContext", err)
	}
}

func TestEmptyKnowerRefused(t *testing.T) {
	r := New(tinyIndex())
	if err := r.check(Scoped(nil, "")); !errors.Is(err, ErrNoKnower) {
		t.Errorf("check(Scoped(nil, \"\")) = %v, want ErrNoKnower", err)
	}
}

// A narrower visibility must fail until the filter exists. Reading it at
// internal instead would show a reader every spoiler while claiming not to.
func TestVisibilityNotYetSupported(t *testing.T) {
	r := New(tinyIndex())
	for _, v := range []world.VisibilityKind{world.VisibilityPublic, world.VisibilitySpoiler} {
		if err := r.check(Omniscient(nil, WithVisibility(v))); !errors.Is(err, ErrVisibilityUnsupported) {
			t.Errorf("visibility %q: check = %v, want ErrVisibilityUnsupported", v, err)
		}
		if err := r.check(Scoped(nil, "char_a", WithVisibility(v))); !errors.Is(err, ErrVisibilityUnsupported) {
			t.Errorf("scoped, visibility %q: check = %v, want ErrVisibilityUnsupported", v, err)
		}
	}
	if err := r.check(Omniscient(nil, WithVisibility(world.VisibilityInternal))); err != nil {
		t.Errorf("internal visibility refused: %v", err)
	}
}

func TestUnknownOutcome(t *testing.T) {
	r := New(tinyIndex())
	for _, wl := range []Worldline{
		{"dec_vale": "burned"},  // not one of the decision's outcomes
		{"dec_nowhere": "held"}, // not a decision at all
		{"char_a": "held"},      // an entity, but not a decision
	} {
		if err := r.check(Omniscient(wl)); !errors.Is(err, ErrUnknownOutcome) {
			t.Errorf("worldline %v: check = %v, want ErrUnknownOutcome", wl, err)
		}
	}
	if err := r.check(Omniscient(Worldline{"dec_vale": "fell"})); err != nil {
		t.Errorf("a declared outcome was refused: %v", err)
	}
}

func TestKnowerMustBeAgent(t *testing.T) {
	r := New(tinyIndex())
	if err := r.check(Scoped(Worldline{"dec_vale": "held"}, "loc_keep")); !errors.Is(err, ErrNotAgent) {
		t.Errorf("a location as knower: check = %v, want ErrNotAgent", err)
	}
	if err := r.check(Scoped(nil, "char_a")); err != nil {
		t.Errorf("a character as knower: %v", err)
	}
	if err := r.check(Scoped(nil, "fac_b")); err != nil {
		t.Errorf("a faction as knower: %v", err)
	}
}

// A knower must exist in the world as the context sees it: a knower absent
// from this worldline cannot be asked what they know in it.
func TestKnowerMustBePresent(t *testing.T) {
	r := New(tinyIndex())
	if err := r.check(Scoped(nil, "char_nobody")); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("unknown knower: check = %v, want ErrUnknownEntity", err)
	}
}

// A worldline may only assign decisions present under the context: a draft
// decision in a canon read, or a decision that exists only in another branch,
// cannot open the entities conditioned on it.
func TestWorldlineDecisionMustBePresent(t *testing.T) {
	idx := tinyIndex()
	idx.Entities = append(idx.Entities,
		index.Entity{ID: "dec_nested", Type: "decision", Status: "canon", Visibility: "internal",
			Outcomes: []string{"yes", "no"}, ValidIn: []index.Condition{{Decision: "dec_vale", Outcome: "held"}}},
		index.Entity{ID: "dec_draft", Type: "decision", Status: "draft", Visibility: "internal",
			Outcomes: []string{"yes", "no"}},
	)
	r := New(idx)
	if err := r.check(Omniscient(Worldline{"dec_nested": "yes"})); !errors.Is(err, ErrUnknownOutcome) {
		t.Errorf("nested decision without its branch: %v, want ErrUnknownOutcome", err)
	}
	if err := r.check(Omniscient(Worldline{"dec_vale": "held", "dec_nested": "yes"})); err != nil {
		t.Errorf("nested decision inside its branch: %v", err)
	}
	if err := r.check(Omniscient(Worldline{"dec_draft": "yes"})); !errors.Is(err, ErrUnknownOutcome) {
		t.Errorf("draft decision in a canon read: %v, want ErrUnknownOutcome", err)
	}
	drafts := WithStatuses(world.StatusDraft, world.StatusCanon)
	if err := r.check(Omniscient(Worldline{"dec_draft": "yes"}, drafts)); err != nil {
		t.Errorf("draft decision in a draft read: %v", err)
	}
}

func TestStatusesValidated(t *testing.T) {
	r := New(tinyIndex())
	if err := r.check(Omniscient(nil, WithStatuses())); !errors.Is(err, ErrUnknownStatus) {
		t.Errorf("no statuses: %v, want ErrUnknownStatus", err)
	}
	if err := r.check(Omniscient(nil, WithStatuses("cannon"))); !errors.Is(err, ErrUnknownStatus) {
		t.Errorf("unknown status: %v, want ErrUnknownStatus", err)
	}
}

// The same bad context gives the same error every time.
func TestCheckErrorDeterministic(t *testing.T) {
	r := New(tinyIndex())
	ctx := Omniscient(Worldline{"dec_a": "x", "dec_b": "x", "dec_c": "x", "dec_d": "x"})
	first := r.check(ctx).Error()
	for range 20 {
		if got := r.check(ctx).Error(); got != first {
			t.Fatalf("error changed between runs: %q vs %q", first, got)
		}
	}
}
