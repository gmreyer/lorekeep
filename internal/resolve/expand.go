package resolve

import (
	"cmp"
	"fmt"
	"slices"
)

// Expand depth limits. A graph view asks for a neighbourhood, not the world:
// past three hops a lore graph is most of itself.
const (
	minExpandDepth = 1
	maxExpandDepth = 3
)

// Graph is the neighbourhood of one entity under a context: the entities
// within reach of Root and the edges walked to reach them.
type Graph struct {
	Root  string      `json:"root"`
	Nodes []Summary   `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphEdge is one edge in its authored direction, under its authored
// relation name, whichever end the walk reached it from. Role is the
// relation's role tag, which graph views bind to.
type GraphEdge struct {
	From     string `json:"from"`
	Relation string `json:"relation"`
	To       string `json:"to"`
	Role     string `json:"role,omitempty"`
}

// Expand walks the graph from id under ctx, up to depth hops, following each
// edge from either end. Only present entities and edges that hold are walked.
//
// depth is clamped to 1–3. relations limits the walk to those relations; an
// empty list means all. Each entry is a declared relation or its inverse
// name, which selects the same edges; anything else is ErrUnknownRelation.
//
// Nodes are the root and every entity reached, sorted by ID. Edges are those
// incident to an entity fewer than depth hops from the root, deduplicated and
// sorted by From, Relation, To.
func (r *Resolver) Expand(ctx Context, id string, depth int, relations []string) (Graph, error) {
	if err := r.check(ctx); err != nil {
		return Graph{}, err
	}
	follow, err := r.relationFilter(relations)
	if err != nil {
		return Graph{}, err
	}
	root, ok := r.entity(ctx, id)
	if !ok {
		return Graph{}, fmt.Errorf("%w: %q", ErrUnknownEntity, id)
	}
	depth = min(max(depth, minExpandDepth), maxExpandDepth)

	seen := map[string]bool{root.ID: true}
	edges := map[GraphEdge]bool{}
	frontier := []string{root.ID}
	for range depth {
		var next []string
		reach := func(from, rel, to, other string) {
			if follow != nil && !follow[rel] {
				return
			}
			edges[GraphEdge{From: from, Relation: rel, To: to, Role: r.relations[rel].Role}] = true
			if !seen[other] {
				seen[other] = true
				next = append(next, other)
			}
		}
		for _, cur := range frontier {
			e := r.entities[cur]
			for i := range e.Edges {
				ed := &e.Edges[i]
				if r.edgeHolds(ctx, cur, ed) {
					reach(cur, ed.Relation, ed.Target, ed.Target)
				}
			}
			for _, in := range r.incoming[cur] {
				if r.edgeHolds(ctx, in.source, in.edge) {
					reach(in.source, in.edge.Relation, cur, in.source)
				}
			}
		}
		frontier = next
	}

	g := Graph{Root: root.ID, Nodes: make([]Summary, 0, len(seen)), Edges: make([]GraphEdge, 0, len(edges))}
	for nid := range seen {
		g.Nodes = append(g.Nodes, summarise(r.entities[nid]))
	}
	slices.SortFunc(g.Nodes, func(a, b Summary) int { return cmp.Compare(a.ID, b.ID) })
	for e := range edges {
		g.Edges = append(g.Edges, e)
	}
	slices.SortFunc(g.Edges, func(a, b GraphEdge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.Relation, b.Relation), cmp.Compare(a.To, b.To))
	})
	return g, nil
}

// relationFilter maps the names a caller asked for to the authored relations
// they select. A nil result means every relation.
func (r *Resolver) relationFilter(names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	follow := make(map[string]bool, len(names))
	for _, name := range names {
		if _, ok := r.relations[name]; ok {
			follow[name] = true
			continue
		}
		forward := ""
		for _, rel := range r.relations {
			if name != "" && rel.Inverse == name {
				forward = rel.Name
				break
			}
		}
		if forward == "" {
			return nil, fmt.Errorf("%w: %q", ErrUnknownRelation, name)
		}
		follow[forward] = true
	}
	return follow, nil
}
