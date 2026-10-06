package resolve

import (
	"testing"

	"github.com/gmreyer/lorekeep/internal/world"
)

func TestContextAccessors(t *testing.T) {
	wl := Worldline{"dec_vale": "held"}
	ctx := Scoped(wl, "char_a")
	if ctx.IsOmniscient() {
		t.Error("a scoped context claims omniscience")
	}
	if k, ok := ctx.Knower(); !ok || k != "char_a" {
		t.Errorf("Knower() = %q, %t", k, ok)
	}

	// The context keeps its own copy: a caller changing the map afterwards
	// must not move a context already handed to the resolver.
	wl["dec_vale"] = "fell"
	if got := ctx.Worldline()["dec_vale"]; got != "held" {
		t.Errorf("worldline changed under the context: %q", got)
	}
	ctx.Worldline()["dec_vale"] = "fell"
	if got := ctx.Worldline()["dec_vale"]; got != "held" {
		t.Errorf("Worldline() leaked the context's map: %q", got)
	}

	o := Omniscient(nil)
	if !o.IsOmniscient() {
		t.Error("Omniscient is not omniscient")
	}
	if _, ok := o.Knower(); ok {
		t.Error("an omniscient context has a knower")
	}
}

func TestStatusesDefaultToCanon(t *testing.T) {
	ctx := Omniscient(nil)
	if !ctx.admits(string(world.StatusCanon)) {
		t.Error("canon is not admitted by default")
	}
	for _, s := range []world.Status{world.StatusDraft, world.StatusDeprecated, world.StatusNonCanon} {
		if ctx.admits(string(s)) {
			t.Errorf("%s is admitted by default", s)
		}
	}
	ctx = Omniscient(nil, WithStatuses(world.StatusDraft, world.StatusCanon))
	if !ctx.admits(string(world.StatusDraft)) || !ctx.admits(string(world.StatusCanon)) {
		t.Error("WithStatuses(draft, canon) does not admit both")
	}
	if ctx.admits(string(world.StatusNonCanon)) {
		t.Error("WithStatuses(draft, canon) admits non_canon")
	}
}
