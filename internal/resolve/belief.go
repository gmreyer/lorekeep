package resolve

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/schema"
)

// Source says where a resolved belief came from.
type Source string

const (
	// Explicit is the agent's own belief.
	Explicit Source = "explicit"
	// Inherited is a faction's belief, reached through a membership.
	Inherited Source = "inherited"
	// Common is canon truth, known because the statement is common knowledge.
	Common Source = "common"
	// Canon is canon truth, read omnisciently.
	Canon Source = "canon"
)

// Belief is a statement as one mind holds it, or as canon has it.
type Belief struct {
	Statement string `json:"statement"`
	Subject   string `json:"subject"`
	Relation  string `json:"relation"`
	Object    string `json:"object"`
	// Held is true, false, or unresolved (canon only).
	Held   string `json:"held"`
	Source Source `json:"source"`
	// Via is the faction a belief was inherited from.
	Via          string `json:"via,omitempty"`
	Confidence   string `json:"confidence,omitempty"`
	AcquiredFrom string `json:"acquired_from,omitempty"`
}

// Beliefs lists what agent believes, one entry per statement present under
// ctx that the agent is not ignorant of, sorted by statement.
//
// A scoped context may read only its own knower's mind: a knower reading
// another agent's beliefs would learn what the story has not told them.
// An omniscient context may read any agent's.
func (r *Resolver) Beliefs(ctx Context, agent string) ([]Belief, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	if !ctx.omniscient && ctx.knower != agent {
		return nil, fmt.Errorf("%w: %q cannot read the beliefs of %q", ErrOutOfScope, ctx.knower, agent)
	}
	a, err := r.agent(ctx, agent)
	if err != nil {
		return nil, err
	}
	out := []Belief{}
	for _, s := range r.presentStatements(ctx) {
		if b, ok := r.resolveBelief(ctx, a, s); ok {
			out = append(out, b)
		}
	}
	return out, nil
}

// statementsAbout lists the present statements whose subject or object is id:
// canon truth for an omniscient context, the knower's resolved belief for a
// scoped one. ctx has been checked, so a scoped knower is present.
func (r *Resolver) statementsAbout(ctx Context, id string) []Belief {
	var knower *index.Entity
	if !ctx.omniscient {
		knower, _ = r.entity(ctx, ctx.knower)
	}
	out := []Belief{}
	for _, s := range r.presentStatements(ctx) {
		if s.Subject != id && s.Object != id {
			continue
		}
		if ctx.omniscient {
			b := triple(s)
			b.Held, b.Source = s.Truth, Canon
			out = append(out, b)
			continue
		}
		if b, ok := r.resolveBelief(ctx, knower, s); ok {
			out = append(out, b)
		}
	}
	return out
}

// presentStatements returns every statement present under ctx, sorted by ID.
func (r *Resolver) presentStatements(ctx Context) []*index.Statement {
	var out []*index.Statement
	for i := range r.idx.Statements {
		if s, ok := r.statement(ctx, r.idx.Statements[i].ID); ok {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *index.Statement) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// resolveBelief runs the belief chain for agent a on present statement s:
//
//  1. a's own belief, if one holds under ctx;
//  2. else the first faction a is a member of, by priority, holding one;
//  3. else canon truth, if s is common knowledge and resolved;
//  4. else ignorance: no belief.
func (r *Resolver) resolveBelief(ctx Context, a *index.Entity, s *index.Statement) (Belief, bool) {
	if b, ok := r.ownBelief(ctx, a, s); ok {
		return b, true
	}
	if b, ok := r.inheritedBelief(ctx, a, s); ok {
		return b, true
	}
	if s.Common && s.Truth != "unresolved" {
		b := triple(s)
		b.Held, b.Source = s.Truth, Common
		return b, true
	}
	return Belief{}, false
}

// ownBelief is rule 1: a's own belief on s that holds under ctx. When several
// hold, the one with the most conditions — the most specific to this
// worldline — wins, then the first authored.
func (r *Resolver) ownBelief(ctx Context, a *index.Entity, s *index.Statement) (Belief, bool) {
	var best *index.Belief
	for i := range a.Beliefs {
		b := &a.Beliefs[i]
		if b.Statement != s.ID || !holds(b.ValidIn, ctx.worldline) {
			continue
		}
		if best == nil || len(b.ValidIn) > len(best.ValidIn) {
			best = b
		}
	}
	if best == nil {
		return Belief{}, false
	}
	out := triple(s)
	out.Held = strconv.FormatBool(best.Value)
	out.Source = Explicit
	out.Confidence = best.Confidence
	// The source is named only if it is present: an absent one would leak.
	if _, ok := r.entity(ctx, best.AcquiredFrom); ok {
		out.AcquiredFrom = best.AcquiredFrom
	}
	return out, true
}

// inheritedBelief is rule 2: the own belief on s of the first faction a is a
// member of, walking memberships by priority. Only memberships that hold
// under ctx, to factions present under it, count.
//
// One level only. A faction inherits from nothing, even when it is itself a
// member of another faction: it reads its own beliefs, then common knowledge.
//
// The faction's confidence travels with the belief; where the faction got it
// from does not, since Via already names the member's source.
func (r *Resolver) inheritedBelief(ctx Context, a *index.Entity, s *index.Statement) (Belief, bool) {
	if a.Type == schema.TypeFaction {
		return Belief{}, false
	}
	for _, ed := range r.memberships(ctx, a) {
		faction, _ := r.entity(ctx, ed.Target)
		if b, ok := r.ownBelief(ctx, faction, s); ok {
			b.Source, b.Via, b.AcquiredFrom = Inherited, faction.ID, ""
			return b, true
		}
	}
	return Belief{}, false
}

// memberships returns a's membership-role edges that hold under ctx, by
// priority: 1 is strongest, and unnumbered edges follow the numbered ones in
// authored order.
func (r *Resolver) memberships(ctx Context, a *index.Entity) []*index.Edge {
	var out []*index.Edge
	for i := range a.Edges {
		ed := &a.Edges[i]
		if r.membership[ed.Relation] && r.edgeHolds(ctx, a.ID, ed) {
			out = append(out, ed)
		}
	}
	slices.SortStableFunc(out, func(x, y *index.Edge) int {
		return comparePriority(x.Priority, y.Priority)
	})
	return out
}

func triple(s *index.Statement) Belief {
	return Belief{Statement: s.ID, Subject: s.Subject, Relation: s.Relation, Object: s.Object}
}
