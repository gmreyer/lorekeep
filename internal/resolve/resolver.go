package resolve

import (
	"fmt"
	"slices"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/schema"
)

// agentGroup names the core group of types that hold beliefs. Like the core
// roles, it is core-owned and closed to renaming.
const agentGroup = "agent"

// Resolver answers scoped reads over one compiled index. It never reads SQLite
// or files, and never changes the index: a consumer that sees the world change
// loads a new index and builds a new Resolver.
type Resolver struct {
	idx        *index.Index
	entities   map[string]*index.Entity
	statements map[string]*index.Statement
	relations  map[string]index.Relation
	agentTypes map[string]bool
	membership map[string]bool // relations carrying the membership role

	// incoming lists, per target, every authored edge pointing at it, so a
	// derived edge is found without a scan of the world.
	incoming map[string][]incomingEdge
}

type incomingEdge struct {
	source string
	edge   *index.Edge
}

// New builds a resolver over idx.
func New(idx *index.Index) *Resolver {
	r := &Resolver{
		idx:        idx,
		entities:   make(map[string]*index.Entity, len(idx.Entities)),
		statements: make(map[string]*index.Statement, len(idx.Statements)),
		relations:  make(map[string]index.Relation, len(idx.Vocabulary.Relations)),
		agentTypes: map[string]bool{},
		membership: map[string]bool{},
		incoming:   map[string][]incomingEdge{},
	}
	for i := range idx.Entities {
		e := &idx.Entities[i]
		r.entities[e.ID] = e
		for j := range e.Edges {
			ed := &e.Edges[j]
			r.incoming[ed.Target] = append(r.incoming[ed.Target], incomingEdge{e.ID, ed})
		}
	}
	for i := range idx.Statements {
		r.statements[idx.Statements[i].ID] = &idx.Statements[i]
	}
	for _, rel := range idx.Vocabulary.Relations {
		r.relations[rel.Name] = rel
		if rel.Role == string(schema.RoleMembership) {
			r.membership[rel.Name] = true
		}
	}
	for _, g := range idx.Vocabulary.Groups {
		if g.Name == agentGroup {
			for _, t := range g.Types {
				r.agentTypes[t] = true
			}
		}
	}
	return r
}

// check validates ctx against the world: every worldline pair names a declared
// decision and one of its outcomes, and a scoped knower is an agent present
// under the context. Every public read starts here.
func (r *Resolver) check(ctx Context) error {
	if err := ctx.valid(); err != nil {
		return err
	}
	for dec, out := range ctx.worldline {
		e, ok := r.entities[dec]
		if !ok || e.Type != schema.TypeDecision {
			return fmt.Errorf("%w: %q is not a decision", ErrUnknownOutcome, dec)
		}
		if !slices.Contains(e.Outcomes, out) {
			return fmt.Errorf("%w: decision %q has no outcome %q", ErrUnknownOutcome, dec, out)
		}
	}
	if !ctx.omniscient {
		if _, err := r.agent(ctx, ctx.knower); err != nil {
			return fmt.Errorf("knower: %w", err)
		}
	}
	return nil
}

// agent returns an agent present under ctx.
func (r *Resolver) agent(ctx Context, id string) (*index.Entity, error) {
	e, ok := r.entity(ctx, id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEntity, id)
	}
	if !r.agentTypes[e.Type] {
		return nil, fmt.Errorf("%w: %q is a %s", ErrNotAgent, id, e.Type)
	}
	return e, nil
}

// entity returns an entity if it is present under ctx: its valid_in holds and
// its status is read.
func (r *Resolver) entity(ctx Context, id string) (*index.Entity, bool) {
	e, ok := r.entities[id]
	if !ok || !holds(e.ValidIn, ctx.worldline) || !ctx.admits(e.Status) {
		return nil, false
	}
	return e, true
}

// statement returns a statement if it is present under ctx.
func (r *Resolver) statement(ctx Context, id string) (*index.Statement, bool) {
	s, ok := r.statements[id]
	if !ok || !holds(s.ValidIn, ctx.worldline) || !ctx.admits(s.Status) {
		return nil, false
	}
	return s, true
}

// edgeHolds reports whether an authored edge exists under ctx: its own
// valid_in holds and both of its ends are present. An edge to a filtered
// entity disappears from both ends, derived inverse included.
func (r *Resolver) edgeHolds(ctx Context, source string, e *index.Edge) bool {
	if !holds(e.ValidIn, ctx.worldline) {
		return false
	}
	_, src := r.entity(ctx, source)
	_, tgt := r.entity(ctx, e.Target)
	return src && tgt
}

// holds reports whether every condition holds under wl. A list is an AND, and
// a condition holds only when wl assigns exactly its outcome; an unassigned
// decision does not hold.
func holds(conds []index.Condition, wl Worldline) bool {
	for _, c := range conds {
		if out, ok := wl[c.Decision]; !ok || out != c.Outcome {
			return false
		}
	}
	return true
}
