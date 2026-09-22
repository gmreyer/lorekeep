// Package schema defines the lore schema pack format: the closed vocabulary of
// entity types, relations, roles, and eras that every other part of the system
// is generated from.
//
// A pack comes in two tiers. The core pack ships embedded in this module and
// holds structural relations only — identity, membership, containment,
// placement, participation, causation, ownership, ordering. A project pack
// lives in a world repo and adds that world's own vocabulary. Merge folds the
// second over the first; a name declared in both is an error, never an
// override, because a collision is how a project learns that one of its
// relations has been promoted upstream.
//
// Nothing here reads world content. The pack is the vocabulary; entity files
// are validated against it elsewhere.
package schema

import "slices"

// Role is a view-binding tag. Graph projections and traversal queries bind to
// a role, never to a relation name, so a world that calls containment
// something else still gets its geography view. The set is closed and declared
// by the core pack; a project pack uses these and may not add to them.
type Role string

// RoleDef declares a role. Only the core pack may hold these.
type RoleDef struct {
	Name        Role   `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// EntityType is one member of the closed set of entity types.
type EntityType struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// Group is a named set of entity types, usable anywhere a type is. Includes
// may name types, other groups, or the single wildcard "*", which expands to
// every type in the merged pack — so a project's new type joins "any"
// automatically.
type Group struct {
	Name        string   `yaml:"name"`
	Includes    []string `yaml:"includes"`
	Description string   `yaml:"description,omitempty"`
}

// Relation declares one edge type.
//
// Direction is not a field: an edge runs source to target, and the direction is
// Domain to Range. Domain and Range are each a list, and the permitted type
// pairs are their cross product.
//
// Inverse is a name only. It is never a second Relation and may never be
// authored on an entity — the index derives it — which is what makes "declared
// once, derived, never authored twice" impossible to violate rather than a
// rule someone has to remember.
type Relation struct {
	Name    string   `yaml:"name"`
	Domain  []string `yaml:"domain"`
	Range   []string `yaml:"range"`
	Inverse string   `yaml:"inverse,omitempty"`
	// Symmetric marks an edge that reads the same both ways. A symmetric
	// relation has no Inverse and its Domain and Range must match.
	Symmetric   bool   `yaml:"symmetric,omitempty"`
	Role        Role   `yaml:"role,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Era is a named interval on the coarse spine of a world's timeline. Order is
// list order in the file: there is no sequence field, so gaps and duplicate
// ordinals cannot be expressed. Eras are world content and appear only in a
// project pack.
type Era struct {
	Key         string   `yaml:"key"`
	Name        string   `yaml:"name,omitempty"`
	Aliases     []string `yaml:"aliases,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

// Pack is a loaded schema pack: core, project, or the two merged.
type Pack struct {
	Name        string
	Version     string
	CoreVersion string // project packs: the core version authored against
	Description string
	Source      Source

	Types     []EntityType
	Groups    []Group
	Roles     []RoleDef
	Relations []Relation
	Eras      []Era

	// Notices are non-blocking observations from the merge.
	Notices []Notice

	res resolved
}

// resolved holds the lookup tables built after parsing. Group expansion needs
// the merged pack, so an unmerged pack resolves only what it can see.
type resolved struct {
	relIdx  map[string]int      // relation name -> index into Relations
	inverse map[string]string   // inverse name -> forward relation name
	types   map[string]struct{} // entity type names, groups excluded
	roles   map[Role]struct{}
	eraOrd  map[string]int
	domain  map[string]map[string]struct{} // relation -> permitted domain types
	rng     map[string]map[string]struct{} // relation -> permitted range types
}

// Relation returns a copy of the named relation. The copy means a caller that
// adjusts what it got back cannot reach into the pack, which matters because
// the core pack is a process-wide singleton.
func (p *Pack) Relation(name string) (*Relation, bool) {
	i, ok := p.res.relIdx[name]
	if !ok {
		return nil, false
	}
	r := cloneRelation(p.Relations[i])
	return &r, true
}

// RelationsWithRole returns every relation carrying the role, in pack order:
// core relations first, then the project's, each keeping file order. This is
// the binding surface for graph projections.
func (p *Pack) RelationsWithRole(role Role) []Relation {
	var out []Relation
	for _, r := range p.Relations {
		if r.Role == role {
			out = append(out, cloneRelation(r))
		}
	}
	return out
}

// HasType reports whether name is a declared entity type. A group is not a
// type: nothing is of type "agent".
func (p *Pack) HasType(name string) bool {
	_, ok := p.res.types[name]
	return ok
}

// HasRole reports whether role is declared.
func (p *Pack) HasRole(role Role) bool {
	_, ok := p.res.roles[role]
	return ok
}

// AllowsDomain reports whether an edge of type rel may start at an entity of
// type typ. Groups in the declaration are already expanded.
func (p *Pack) AllowsDomain(rel, typ string) bool {
	return allows(p.res.domain, rel, typ)
}

// AllowsRange reports whether an edge of type rel may point at an entity of
// type typ.
func (p *Pack) AllowsRange(rel, typ string) bool {
	return allows(p.res.rng, rel, typ)
}

func allows(sets map[string]map[string]struct{}, rel, typ string) bool {
	set, ok := sets[rel]
	if !ok {
		return false
	}
	_, ok = set[typ]
	return ok
}

// IsInverseName reports whether name is a derived inverse, and if so names the
// forward relation it belongs to. Authoring an inverse on an entity is an
// error, and traversing one runs the forward relation backwards.
//
// A symmetric relation is its own inverse but has no derived name, so it is
// not reported here.
func (p *Pack) IsInverseName(name string) (string, bool) {
	fwd, ok := p.res.inverse[name]
	return fwd, ok
}

// EraOrdinal returns an era's position on the timeline, counting from zero in
// file order.
func (p *Pack) EraOrdinal(key string) (int, bool) {
	i, ok := p.res.eraOrd[key]
	return i, ok
}

// index builds the lookup tables from whatever the pack currently holds.
// Called at the end of a load and again at the end of a merge.
func (p *Pack) index() {
	p.res = resolved{
		relIdx:  make(map[string]int, len(p.Relations)),
		inverse: make(map[string]string),
		types:   make(map[string]struct{}, len(p.Types)),
		roles:   make(map[Role]struct{}, len(p.Roles)),
		eraOrd:  make(map[string]int, len(p.Eras)),
		domain:  make(map[string]map[string]struct{}, len(p.Relations)),
		rng:     make(map[string]map[string]struct{}, len(p.Relations)),
	}
	for i, r := range p.Relations {
		if _, seen := p.res.relIdx[r.Name]; !seen {
			p.res.relIdx[r.Name] = i
		}
		if r.Inverse != "" {
			if _, seen := p.res.inverse[r.Inverse]; !seen {
				p.res.inverse[r.Inverse] = r.Name
			}
		}
	}
	for _, t := range p.Types {
		p.res.types[t.Name] = struct{}{}
	}
	for _, r := range p.Roles {
		p.res.roles[r.Name] = struct{}{}
	}
	for i, e := range p.Eras {
		if _, seen := p.res.eraOrd[e.Key]; !seen {
			p.res.eraOrd[e.Key] = i
		}
	}

	expand := p.groupExpansion()
	for _, r := range p.Relations {
		p.res.domain[r.Name] = p.resolveTypes(r.Domain, expand)
		p.res.rng[r.Name] = p.resolveTypes(r.Range, expand)
	}
}

// resolveTypes turns a declared domain or range list into the concrete entity
// types it permits. Names that resolve to nothing are dropped; the merge has
// already reported them.
func (p *Pack) resolveTypes(names []string, expand map[string][]string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, n := range names {
		if _, ok := p.res.types[n]; ok {
			out[n] = struct{}{}
			continue
		}
		for _, t := range expand[n] {
			out[t] = struct{}{}
		}
	}
	return out
}

// groupExpansion flattens every group to the concrete entity types it covers.
// A group that cycles resolves to nothing; the merge reports the cycle.
func (p *Pack) groupExpansion() map[string][]string {
	byName := make(map[string]Group, len(p.Groups))
	for _, g := range p.Groups {
		if _, seen := byName[g.Name]; !seen {
			byName[g.Name] = g
		}
	}

	out := make(map[string][]string, len(p.Groups))
	var visit func(name string, seen map[string]bool) []string
	visit = func(name string, seen map[string]bool) []string {
		if done, ok := out[name]; ok {
			return done
		}
		if seen[name] {
			return nil // cycle
		}
		g, ok := byName[name]
		if !ok {
			return nil
		}
		seen[name] = true
		defer delete(seen, name)

		var types []string
		for _, inc := range g.Includes {
			switch {
			case inc == wildcard:
				for _, t := range p.Types {
					types = append(types, t.Name)
				}
			case p.HasType(inc):
				types = append(types, inc)
			default:
				types = append(types, visit(inc, seen)...)
			}
		}
		slices.Sort(types)
		types = slices.Compact(types)
		out[name] = types
		return types
	}

	for _, g := range p.Groups {
		visit(g.Name, map[string]bool{})
	}
	return out
}

func cloneRelation(r Relation) Relation {
	r.Domain = slices.Clone(r.Domain)
	r.Range = slices.Clone(r.Range)
	return r
}

// wildcard is the one reserved token in a group's Includes. It expands after
// the merge, so a project's new entity type joins "any" without being listed.
const wildcard = "*"
