package resolve

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/gmreyer/lorekeep/internal/index"
)

// DivergenceKind says how a mind departs from what it should hold.
type DivergenceKind string

const (
	// Heretic is an agent whose own belief contradicts the faction it would
	// otherwise inherit from.
	Heretic DivergenceKind = "heretic"
	// Deceived is an agent whose resolved belief contradicts canon truth.
	Deceived DivergenceKind = "deceived"
	// Liar is an agent asserting a value it does not believe.
	Liar DivergenceKind = "liar"
)

// againstCanon is the Against of a Deceived divergence.
const againstCanon = "canon"

// Divergence is one place where an agent's mind or word departs from a
// reference: its faction, canon truth, or its own belief.
type Divergence struct {
	Agent     string         `json:"agent"`
	Statement string         `json:"statement"`
	Kind      DivergenceKind `json:"kind"`
	// Held is what the agent believes.
	Held string `json:"held"`
	// Expected is the value it is compared against: the faction's belief,
	// canon truth, or what the agent asserted.
	Expected string `json:"expected"`
	// Against is the faction for a heretic, "canon" for the deceived, and
	// the audience for a liar — empty when the lie is open or its audience
	// is absent under the context.
	Against string `json:"against,omitempty"`
}

// Divergence lists the heretics, the deceived and the liars among agents, or
// among every agent present under ctx when none is named, sorted by agent,
// statement and kind.
//
// It compares minds against canon truth and against each other, so it is an
// authoring read: a scoped context gets ErrOutOfScope.
func (r *Resolver) Divergence(ctx Context, agents ...string) ([]Divergence, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	if !ctx.omniscient {
		return nil, fmt.Errorf("%w: divergence compares against canon truth", ErrOutOfScope)
	}
	var minds []*index.Entity
	if len(agents) == 0 {
		for i := range r.idx.Entities {
			id := r.idx.Entities[i].ID
			if a, err := r.agent(ctx, id); err == nil {
				minds = append(minds, a)
			}
		}
	} else {
		for _, id := range agents {
			a, err := r.agent(ctx, id)
			if err != nil {
				return nil, err
			}
			minds = append(minds, a)
		}
	}
	statements := r.presentStatements(ctx)
	var out []Divergence
	for _, a := range minds {
		out = append(out, r.divergences(ctx, a, statements)...)
	}
	slices.SortFunc(out, func(x, y Divergence) int {
		return cmp.Or(
			cmp.Compare(x.Agent, y.Agent),
			cmp.Compare(x.Statement, y.Statement),
			cmp.Compare(x.Kind, y.Kind),
			cmp.Compare(x.Against, y.Against),
			cmp.Compare(x.Expected, y.Expected),
		)
	})
	// An agent named twice, or a lie told twice to the same audience, is
	// one divergence.
	if out = slices.Compact(out); out == nil {
		out = []Divergence{}
	}
	return out, nil
}

// divergences finds a's divergences on the present statements.
func (r *Resolver) divergences(ctx Context, a *index.Entity, statements []*index.Statement) []Divergence {
	var out []Divergence
	for _, s := range statements {
		own, hasOwn := r.ownBelief(ctx, a, s)
		if inh, ok := r.inheritedBelief(ctx, a, s); hasOwn && ok && own.Held != inh.Held {
			out = append(out, Divergence{
				Agent: a.ID, Statement: s.ID, Kind: Heretic,
				Held: own.Held, Expected: inh.Held, Against: inh.Via,
			})
		}
		b, ok := r.resolveBelief(ctx, a, s)
		if ok && resolved(b.Held) && resolved(s.Truth) && b.Held != s.Truth {
			out = append(out, Divergence{
				Agent: a.ID, Statement: s.ID, Kind: Deceived,
				Held: b.Held, Expected: s.Truth, Against: againstCanon,
			})
		}
		if !ok {
			continue
		}
		for _, as := range a.Assertions {
			said := strconv.FormatBool(as.Value)
			if as.Statement != s.ID || !r.assertionHolds(ctx, as) || said == b.Held {
				continue
			}
			out = append(out, Divergence{Agent: a.ID, Statement: s.ID, Kind: Liar,
				Held: b.Held, Expected: said, Against: as.Audience})
		}
	}
	return out
}

// assertionHolds reports whether an assertion is made under ctx: its valid_in
// holds, its statement is present, and its audience, if any, is present. A
// lie told to someone absent from this worldline is not told in it.
func (r *Resolver) assertionHolds(ctx Context, as index.Assertion) bool {
	if !holds(as.ValidIn, ctx.worldline) {
		return false
	}
	if _, ok := r.statement(ctx, as.Statement); !ok {
		return false
	}
	if as.Audience != "" {
		if _, ok := r.entity(ctx, as.Audience); !ok {
			return false
		}
	}
	return true
}

// resolved reports whether a truth value is true or false, not unresolved.
func resolved(v string) bool { return v == "true" || v == "false" }
