// Package index compiles a validated world into the two derived artefacts: a
// SQLite index for the authoring side, and a JSON snapshot for the runtime.
//
// Both are disposable. They are rebuilt from the repository and never edited
// in place, which is what lets the graph technology change later without
// migrating the source of truth. The files are the truth; this is a cache with
// a build step.
//
// There is one normalisation and two serialisers. Index below is the shape the
// snapshot ships, because that is the shape a runtime with no query engine
// wants — everything an entity owns, on the entity. The SQLite writer
// flattens the same model into tables, because that is the shape a query
// engine wants. Building the model once means the two can never disagree about
// what the world contains.
package index

import (
	"github.com/gmreyer/lore-core/internal/schema"
	"github.com/gmreyer/lore-core/internal/world"
)

// Format is the snapshot's own version. A runtime reads it before anything
// else and refuses a snapshot it does not understand, so that a stale build
// shipped by accident fails loudly instead of loading half a world.
const Format = 1

// Index is the complete derived world.
type Index struct {
	Format     int         `json:"format"`
	Pack       PackInfo    `json:"pack"`
	Vocabulary Vocabulary  `json:"vocabulary"`
	Entities   []Entity    `json:"entities"`
	Statements []Statement `json:"statements"`
}

// PackInfo records which vocabulary this world was built against. Nothing
// time-varying goes in here: a build of an unchanged repository produces
// identical bytes, which is what makes a golden diff meaningful and a rebuild
// verifiable.
type PackInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	CoreVersion string `json:"core_version"`
}

// Vocabulary is the merged schema pack, shipped with the world.
//
// The runtime carries it because everything downstream is schema-driven: a
// traversal binds to a role, not to a relation name, and it can only do that
// if it can see which relations carry which role.
type Vocabulary struct {
	EntityTypes []NamedThing `json:"entity_types"`
	Groups      []GroupInfo  `json:"groups"`
	Roles       []NamedThing `json:"roles"`
	Relations   []Relation   `json:"relations"`
	Eras        []Ordered    `json:"eras"`
	Acts        []Ordered    `json:"acts"`
}

type NamedThing struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type GroupInfo struct {
	Name  string   `json:"name"`
	Types []string `json:"types"`
}

// Relation carries the inverse as a name only. Inverse rows are not
// materialised anywhere: an inverse is traversed by running the forward
// relation backwards, which is what keeps "declared once, derived, never
// authored twice" true of the index as well as of the files.
type Relation struct {
	Name        string   `json:"name"`
	Domain      []string `json:"domain"`
	Range       []string `json:"range"`
	Inverse     string   `json:"inverse,omitempty"`
	Symmetric   bool     `json:"symmetric,omitempty"`
	Role        string   `json:"role,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Ordered is an era or an act: a key whose position carries the meaning.
type Ordered struct {
	Key     string `json:"key"`
	Name    string `json:"name,omitempty"`
	Ordinal int    `json:"ordinal"`
}

type Entity struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Name       string      `json:"name"`
	Aliases    []string    `json:"aliases,omitempty"`
	Status     string      `json:"status"`
	Visibility string      `json:"visibility"`
	Interval   *Interval   `json:"interval,omitempty"`
	Outcomes   []string    `json:"outcomes,omitempty"`
	ValidIn    []Condition `json:"valid_in,omitempty"`
	Edges      []Edge      `json:"edges,omitempty"`
	Beliefs    []Belief    `json:"beliefs,omitempty"`
	Assertions []Assertion `json:"assertions,omitempty"`
	File       string      `json:"file"`
	Body       string      `json:"body,omitempty"`
}

// Interval is an entity's position in time: a character's lifespan or an
// event's date. One field serves both, because an entity has at most one — the
// validator sees to that — and the type says which it is.
type Interval struct {
	Era       string `json:"era,omitempty"`
	Earliest  *int   `json:"earliest,omitempty"`
	Latest    *int   `json:"latest,omitempty"`
	Precision string `json:"precision,omitempty"`
}

// Edge carries a stable ID so that the flat tables can hang a condition off
// it. IDs are assigned in walk order and mean nothing outside one build.
type Edge struct {
	ID       int         `json:"id"`
	Relation string      `json:"relation"`
	Target   string      `json:"target"`
	Priority *int        `json:"priority,omitempty"`
	ValidIn  []Condition `json:"valid_in,omitempty"`
	Note     string      `json:"note,omitempty"`
}

type Belief struct {
	ID           int         `json:"id"`
	Statement    string      `json:"statement"`
	Value        bool        `json:"value"`
	Confidence   string      `json:"confidence,omitempty"`
	Since        *Interval   `json:"since,omitempty"`
	AcquiredFrom string      `json:"acquired_from,omitempty"`
	ValidIn      []Condition `json:"valid_in,omitempty"`
}

type Assertion struct {
	ID        int         `json:"id"`
	Statement string      `json:"statement"`
	Value     bool        `json:"value"`
	Audience  string      `json:"audience,omitempty"`
	When      *Interval   `json:"when,omitempty"`
	ValidIn   []Condition `json:"valid_in,omitempty"`
}

type Condition struct {
	Decision string `json:"decision"`
	Outcome  string `json:"outcome"`
}

type Statement struct {
	ID         string      `json:"id"`
	Name       string      `json:"name,omitempty"`
	Subject    string      `json:"subject"`
	Relation   string      `json:"relation"`
	Object     string      `json:"object"`
	Truth      string      `json:"truth"`
	Status     string      `json:"status"`
	Visibility string      `json:"visibility"`
	ValidIn    []Condition `json:"valid_in,omitempty"`
	File       string      `json:"file"`
	Body       string      `json:"body,omitempty"`
}

// Compile turns a loaded world and its merged pack into the derived model.
//
// It assumes the world has already validated: nothing here reports a fault,
// and a dangling reference would simply be copied through. Build is the
// entry point that enforces that order.
func Compile(w *world.World, pack *schema.Pack) *Index {
	idx := &Index{
		Format: Format,
		Pack: PackInfo{
			Name:        pack.Name,
			Version:     pack.Version,
			CoreVersion: pack.CoreVersion,
		},
		Vocabulary: compileVocabulary(pack),
	}

	// One counter per kind, advanced in walk order, so a rebuild of an
	// unchanged repository assigns the same ids.
	var edgeID, beliefID, assertID int

	for _, e := range w.Entities {
		out := Entity{
			ID:         e.ID,
			Type:       e.Type,
			Name:       e.Name,
			Aliases:    e.Aliases,
			Status:     string(e.Status),
			Visibility: e.Visibility.String(),
			Interval:   compileInterval(intervalOf(e)),
			Outcomes:   e.Outcomes,
			ValidIn:    compileConditions(e.ValidIn),
			File:       e.Source.File,
			Body:       e.Body,
		}

		for _, r := range e.Relations {
			edgeID++
			out.Edges = append(out.Edges, Edge{
				ID:       edgeID,
				Relation: r.Type,
				Target:   r.Target,
				Priority: r.Priority,
				ValidIn:  compileConditions(r.ValidIn),
				Note:     r.Note,
			})
		}
		for _, b := range e.Beliefs {
			beliefID++
			out.Beliefs = append(out.Beliefs, Belief{
				ID:           beliefID,
				Statement:    b.Statement,
				Value:        b.Value != nil && *b.Value,
				Confidence:   string(b.Confidence),
				Since:        compileInterval(b.Since),
				AcquiredFrom: b.AcquiredFrom,
				ValidIn:      compileConditions(b.ValidIn),
			})
		}
		for _, a := range e.Asserts {
			assertID++
			out.Assertions = append(out.Assertions, Assertion{
				ID:        assertID,
				Statement: a.Statement,
				Value:     a.Value != nil && *a.Value,
				Audience:  a.To,
				When:      compileInterval(a.When),
				ValidIn:   compileConditions(a.ValidIn),
			})
		}
		idx.Entities = append(idx.Entities, out)
	}

	for _, s := range w.Statements {
		idx.Statements = append(idx.Statements, Statement{
			ID:         s.ID,
			Name:       s.Name,
			Subject:    s.Subject,
			Relation:   s.Relation,
			Object:     s.Object,
			Truth:      string(s.Truth),
			Status:     string(s.Status),
			Visibility: s.Visibility.String(),
			ValidIn:    compileConditions(s.ValidIn),
			File:       s.Source.File,
			Body:       s.Body,
		})
	}

	return idx
}

// intervalOf picks whichever interval the entity carries. A lifespan and a
// date never coexist — a field on the wrong type is a blocking error — so
// there is nothing to choose between.
func intervalOf(e *world.Entity) *world.Interval {
	if e.Lifespan != nil {
		return e.Lifespan
	}
	return e.Date
}

func compileInterval(iv *world.Interval) *Interval {
	if iv == nil {
		return nil
	}
	return &Interval{
		Era:       iv.Era,
		Earliest:  iv.Earliest,
		Latest:    iv.Latest,
		Precision: string(iv.Precision),
	}
}

func compileConditions(conds []world.Condition) []Condition {
	if len(conds) == 0 {
		return nil
	}
	out := make([]Condition, 0, len(conds))
	for _, c := range conds {
		out = append(out, Condition{Decision: c.Decision, Outcome: c.Outcome})
	}
	return out
}

func compileVocabulary(pack *schema.Pack) Vocabulary {
	v := Vocabulary{}
	for _, t := range pack.Types {
		v.EntityTypes = append(v.EntityTypes, NamedThing{Name: t.Name, Description: t.Description})
	}
	for _, g := range pack.Groups {
		types, _ := pack.ExpandGroup(g.Name)
		v.Groups = append(v.Groups, GroupInfo{Name: g.Name, Types: types})
	}
	for _, r := range pack.Roles {
		v.Roles = append(v.Roles, NamedThing{Name: string(r.Name), Description: r.Description})
	}
	for _, r := range pack.Relations {
		v.Relations = append(v.Relations, Relation{
			Name:        r.Name,
			Domain:      r.Domain,
			Range:       r.Range,
			Inverse:     r.Inverse,
			Symmetric:   r.Symmetric,
			Role:        string(r.Role),
			Description: r.Description,
		})
	}
	for i, e := range pack.Eras {
		v.Eras = append(v.Eras, Ordered{Key: e.Key, Name: e.Name, Ordinal: i})
	}
	for i, a := range pack.Acts {
		v.Acts = append(v.Acts, Ordered{Key: a.Key, Name: a.Name, Ordinal: i})
	}
	return v
}
