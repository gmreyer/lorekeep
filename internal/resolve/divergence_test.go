package resolve

import (
	"errors"
	"slices"
	"testing"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/google/go-cmp/cmp"
)

// divergences runs an omniscient Divergence under wl and fails on error.
func divergences(t *testing.T, r *Resolver, wl Worldline, agents ...string) []Divergence {
	t.Helper()
	got, err := r.Divergence(Omniscient(wl), agents...)
	if err != nil {
		t.Fatalf("Divergence(%v): %v", agents, err)
	}
	return got
}

func TestHereticFound(t *testing.T) {
	r := fixture(t)
	got := divergences(t, r, nil, "char_miren")
	want := Divergence{
		Agent: "char_miren", Statement: "stmt_miren_oath", Kind: Heretic,
		Held: "false", Expected: "true", Against: "fac_court",
	}
	if !slices.Contains(got, want) {
		t.Errorf("Miren's divergences = %+v, want %+v among them", got, want)
	}
}

func TestDeceivedFound(t *testing.T) {
	r := fixture(t)
	got := divergences(t, r, nil, "char_miren", "fac_order")
	for _, want := range []Divergence{
		{Agent: "char_miren", Statement: "stmt_miren_oath", Kind: Deceived, Held: "false", Expected: "true", Against: "canon"},
		{Agent: "fac_order", Statement: "stmt_heir_lives", Kind: Deceived, Held: "false", Expected: "true", Against: "canon"},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("divergences = %+v, want %+v among them", got, want)
		}
	}
}

func TestLiarFound(t *testing.T) {
	r := fixture(t)
	got := divergences(t, r, nil, "char_vesk")
	want := []Divergence{{
		Agent: "char_vesk", Statement: "stmt_vesk_sworn", Kind: Liar,
		Held: "true", Expected: "false", Against: "",
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Vesk's divergences (-want +got):\n%s", diff)
	}
}

func TestLiarNamesPresentAudience(t *testing.T) {
	base := fixture(t).idx
	idx := *base
	idx.Entities = slices.Clone(base.Entities)
	for i := range idx.Entities {
		if idx.Entities[i].ID == "char_vesk" {
			idx.Entities[i].Assertions = []index.Assertion{
				{Statement: "stmt_vesk_sworn", Value: false, Audience: "char_kaelen"},
				{Statement: "stmt_vesk_sworn", Value: false, Audience: "loc_vale_keep"},
			}
		}
	}
	got := divergences(t, New(&idx), nil, "char_vesk")
	// A lie to an audience absent under the context is not told in it.
	want := []Divergence{
		{Agent: "char_vesk", Statement: "stmt_vesk_sworn", Kind: Liar, Held: "true", Expected: "false", Against: "char_kaelen"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("baseline: a lie to an absent audience shown (-want +got):\n%s", diff)
	}
}

func TestConformistHasNoDivergence(t *testing.T) {
	r := fixture(t)
	for _, agent := range []string{"char_conformist", "char_kaelen", "char_ward"} {
		if got := divergences(t, r, nil, agent); len(got) != 0 {
			t.Errorf("%s: divergences = %+v, want none", agent, got)
		}
	}
}

func TestDivergenceEveryAgent(t *testing.T) {
	r := fixture(t)
	want := []Divergence{
		{Agent: "char_miren", Statement: "stmt_miren_oath", Kind: Deceived, Held: "false", Expected: "true", Against: "canon"},
		{Agent: "char_miren", Statement: "stmt_miren_oath", Kind: Heretic, Held: "false", Expected: "true", Against: "fac_court"},
		{Agent: "char_vesk", Statement: "stmt_vesk_sworn", Kind: Liar, Held: "true", Expected: "false", Against: ""},
		{Agent: "fac_order", Statement: "stmt_heir_lives", Kind: Deceived, Held: "false", Expected: "true", Against: "canon"},
	}
	if diff := cmp.Diff(want, divergences(t, r, nil)); diff != "" {
		t.Errorf("every agent (-want +got):\n%s", diff)
	}
}

func TestDivergenceRequiresOmniscient(t *testing.T) {
	r := fixture(t)
	if _, err := r.Divergence(Scoped(nil, "char_miren"), "char_miren"); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("scoped: err = %v, want ErrOutOfScope", err)
	}
	if _, err := r.Divergence(Scoped(nil, "char_miren")); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("scoped, every agent: err = %v, want ErrOutOfScope", err)
	}
	if _, err := r.Divergence(Context{}); !errors.Is(err, ErrNoContext) {
		t.Errorf("zero context: err = %v, want ErrNoContext", err)
	}
	if _, err := r.Divergence(Omniscient(nil), "loc_vale"); !errors.Is(err, ErrNotAgent) {
		t.Errorf("a location: err = %v, want ErrNotAgent", err)
	}
	if _, err := r.Divergence(Omniscient(nil), "nobody"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("unknown agent: err = %v, want ErrUnknownEntity", err)
	}
	if _, err := r.Divergence(Omniscient(nil), "fac_draft"); !errors.Is(err, ErrUnknownEntity) {
		t.Errorf("absent (draft) agent: err = %v, want ErrUnknownEntity", err)
	}
}
