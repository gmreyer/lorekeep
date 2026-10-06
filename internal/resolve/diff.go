package resolve

import (
	"cmp"
	"fmt"
	"slices"
)

// Fact kinds.
const (
	FactEntity    = "entity"
	FactEdge      = "edge"
	FactStatement = "statement"
	FactBelief    = "belief"
)

// Fact is one authored fact present under a context. Fields a kind does not
// use stay empty:
//
//   - entity: Subject is the entity ID.
//   - edge: Subject, Relation, Object are the source, the authored relation
//     and the target.
//   - statement: Subject is the statement ID, Value its truth.
//   - belief: Subject is the agent, Object the statement, Value the held
//     value. Only an agent's own belief is a fact; inherited and common
//     beliefs follow from other facts.
type Fact struct {
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Relation string `json:"relation,omitempty"`
	Object   string `json:"object,omitempty"`
	Value    string `json:"value,omitempty"`
}

// Diff is what two contexts disagree on: the facts present only under the
// first, and only under the second, each sorted.
type Diff struct {
	OnlyA []Fact `json:"only_a"`
	OnlyB []Fact `json:"only_b"`
}

// Diff compares the authored facts present under a and b. Both must be
// omniscient: comparing endings is an authoring read, and a scoped diff
// would reveal what lies outside the knower's story. Derived edges are not
// facts; each follows from an authored one already compared.
func (r *Resolver) Diff(a, b Context) (Diff, error) {
	for _, c := range []Context{a, b} {
		if err := r.check(c); err != nil {
			return Diff{}, err
		}
		if !c.omniscient {
			return Diff{}, fmt.Errorf("%w: a diff needs omniscient contexts", ErrOutOfScope)
		}
	}
	fa, fb := r.facts(a), r.facts(b)
	return Diff{OnlyA: minus(fa, fb), OnlyB: minus(fb, fa)}, nil
}

// facts returns every authored fact present under ctx, as a set.
func (r *Resolver) facts(ctx Context) map[Fact]bool {
	out := map[Fact]bool{}
	for i := range r.idx.Entities {
		e, ok := r.entity(ctx, r.idx.Entities[i].ID)
		if !ok {
			continue
		}
		out[Fact{Kind: FactEntity, Subject: e.ID}] = true
		for j := range e.Edges {
			ed := &e.Edges[j]
			if r.edgeHolds(ctx, e.ID, ed) {
				out[Fact{Kind: FactEdge, Subject: e.ID, Relation: ed.Relation, Object: ed.Target}] = true
			}
		}
	}
	stmts := r.presentStatements(ctx)
	for _, s := range stmts {
		out[Fact{Kind: FactStatement, Subject: s.ID, Value: s.Truth}] = true
	}
	for i := range r.idx.Entities {
		a, ok := r.entity(ctx, r.idx.Entities[i].ID)
		if !ok || !r.agentTypes[a.Type] {
			continue
		}
		for _, s := range stmts {
			if b, ok := r.ownBelief(ctx, a, s); ok {
				out[Fact{Kind: FactBelief, Subject: a.ID, Object: s.ID, Value: b.Held}] = true
			}
		}
	}
	return out
}

// minus returns the facts in x and not in y, sorted.
func minus(x, y map[Fact]bool) []Fact {
	var out []Fact
	for f := range x {
		if !y[f] {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(p, q Fact) int {
		return cmp.Or(
			cmp.Compare(p.Kind, q.Kind),
			cmp.Compare(p.Subject, q.Subject),
			cmp.Compare(p.Relation, q.Relation),
			cmp.Compare(p.Object, q.Object),
			cmp.Compare(p.Value, q.Value),
		)
	})
	return out
}
