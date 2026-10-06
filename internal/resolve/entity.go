package resolve

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/gmreyer/lorekeep/internal/index"
)

// Summary is the short form of an entity: enough to list and link it.
type Summary struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Status  string   `json:"status"`
}

// Direction says how an edge reached the entity it is shown on.
type Direction string

const (
	// Out is an edge as authored, on its source.
	Out Direction = "out"
	// Derived is an inverse or symmetric edge, computed on the target of the
	// authored one. Its Relation is the inverse name, or for a symmetric
	// relation the relation itself.
	Derived Direction = "derived"
	// In is a one-way relation seen from its target, under its own name.
	In Direction = "in"
)

// Edge is one edge as the entity it is shown on sees it. Target is the other
// end, whichever direction the edge runs.
type Edge struct {
	Relation  string    `json:"relation"`
	Target    string    `json:"target"`
	Direction Direction `json:"direction"`
	Role      string    `json:"role,omitempty"`
	Priority  *int      `json:"priority,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// Entity is one entity as the context sees it: only edges and statements that
// exist under it, and statements as the knower believes them.
type Entity struct {
	Summary
	Visibility string          `json:"visibility"`
	Interval   *index.Interval `json:"interval,omitempty"`
	Outcomes   []string        `json:"outcomes,omitempty"`
	Body       string          `json:"body,omitempty"`
	File       string          `json:"file"`
	Edges      []Edge          `json:"edges"`
	// Statements are the present statements whose subject or object is this
	// entity: canon truth for an omniscient context, the knower's belief for a
	// scoped one, with what the knower is ignorant of left out.
	Statements []Belief `json:"statements"`
}

// Entities lists every entity present under ctx, sorted by ID.
func (r *Resolver) Entities(ctx Context) ([]Summary, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	out := []Summary{}
	for i := range r.idx.Entities {
		if e, ok := r.entity(ctx, r.idx.Entities[i].ID); ok {
			out = append(out, summarise(e))
		}
	}
	slices.SortFunc(out, func(a, b Summary) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// Entity reads one entity under ctx.
func (r *Resolver) Entity(ctx Context, id string) (Entity, error) {
	if err := r.check(ctx); err != nil {
		return Entity{}, err
	}
	e, ok := r.entity(ctx, id)
	if !ok {
		return Entity{}, fmt.Errorf("%w: %q", ErrUnknownEntity, id)
	}
	out := Entity{
		Summary:    summarise(e),
		Visibility: e.Visibility,
		Interval:   cloneInterval(e.Interval),
		Outcomes:   slices.Clone(e.Outcomes),
		Body:       e.Body,
		File:       e.File,
		Edges:      r.edges(ctx, e),
		Statements: r.statementsAbout(ctx, id),
	}
	return out, nil
}

// edges returns every edge of e present under ctx: its authored edges, and the
// derived or inbound view of every edge pointing at it.
func (r *Resolver) edges(ctx Context, e *index.Entity) []Edge {
	out := []Edge{}
	for i := range e.Edges {
		ed := &e.Edges[i]
		if !r.edgeHolds(ctx, e.ID, ed) {
			continue
		}
		out = append(out, Edge{
			Relation:  ed.Relation,
			Target:    ed.Target,
			Direction: Out,
			Role:      r.relations[ed.Relation].Role,
			Priority:  clonePtr(ed.Priority),
			Note:      ed.Note,
		})
	}
	for _, in := range r.incoming[e.ID] {
		if !r.edgeHolds(ctx, in.source, in.edge) {
			continue
		}
		rel := r.relations[in.edge.Relation]
		name, dir := rel.Name, In
		switch {
		case rel.Symmetric:
			dir = Derived
		case rel.Inverse != "":
			name, dir = rel.Inverse, Derived
		}
		out = append(out, Edge{
			Relation:  name,
			Target:    in.source,
			Direction: dir,
			Role:      rel.Role,
			Priority:  clonePtr(in.edge.Priority),
			Note:      in.edge.Note,
		})
	}
	slices.SortFunc(out, compareEdges)
	return out
}

// compareEdges orders edges by relation, target and direction, then — for
// the same fact authored more than once — numbered priority before none, and
// note.
func compareEdges(a, b Edge) int {
	return cmp.Or(
		cmp.Compare(a.Relation, b.Relation),
		cmp.Compare(a.Target, b.Target),
		cmp.Compare(a.Direction, b.Direction),
		comparePriority(a.Priority, b.Priority),
		cmp.Compare(a.Note, b.Note),
	)
}

// comparePriority orders priorities ascending, with an unnumbered one last.
func comparePriority(a, b *int) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*a, *b)
}

// summarise copies what a Summary shares with the index, so a caller that
// changes a returned value cannot reach the resolver's state.
func summarise(e *index.Entity) Summary {
	return Summary{ID: e.ID, Type: e.Type, Name: e.Name, Aliases: slices.Clone(e.Aliases), Status: e.Status}
}

func cloneInterval(iv *index.Interval) *index.Interval {
	if iv == nil {
		return nil
	}
	out := *iv
	out.Earliest, out.Latest = clonePtr(iv.Earliest), clonePtr(iv.Latest)
	return &out
}

func clonePtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
