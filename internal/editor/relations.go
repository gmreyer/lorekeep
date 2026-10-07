package editor

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

// The relations panel (Editor design rounds 2, 3 and 5): the entity's edges
// grouped by role, its authored ones editable on the row, and the
// target-first picker that adds one.

func (s *Server) relationRoutes() {
	s.mux.HandleFunc("POST /entity/{id}/relations/{i}/remove", s.relationRemove)
	s.mux.HandleFunc("POST /entity/{id}/relations/{i}/priority", s.relationPriority)
	s.mux.HandleFunc("POST /entity/{id}/relations/{i}/note", s.relationNote)
}

// relPanel is everything the relations panel shows, read before it renders.
type relPanel struct {
	ID   string
	Tabs Tabs
	// Groups are the rows by role group: membership, containment, other.
	Groups []relGroup
	// Err is a refused edit, shown at the top of the panel.
	Err string
}

// relGroup is one role group of rows.
type relGroup struct {
	Label string
	Rows  []relRow
}

// relRow is one edge as the panel shows it.
type relRow struct {
	// Index is the relation's place in the authored list of the draft, or -1
	// for a derived or inbound edge, which is read only.
	Index    int
	Relation string
	// Class colours the relation name by its role.
	Class string
	// Target is the other end; Name is what the writer sees of it. Linked is
	// false when the target is not present under the worldline: the row then
	// shows Name as a placeholder, never the ID.
	Target string
	Name   string
	Linked bool
	// Priority and Note are the authored values, Priority as typed.
	Priority string
	Note     string
	Conds    []world.Condition
	// From says where a read-only row comes from.
	From string
}

// Role groups, in panel order. Rows bind to the role tag, never to a
// relation name.
var relGroupOrder = []schema.Role{schema.RoleMembership, schema.RoleContainment}

// otherGroup labels every edge whose role is neither membership nor
// containment, untagged ones included.
const otherGroup = "other"

// relClass is the CSS class that colours a relation by its role.
func relClass(role schema.Role) string {
	switch role {
	case schema.RoleMembership:
		return "rel-membership"
	case schema.RoleContainment:
		return "rel-containment"
	case schema.RoleSoftLink:
		return "rel-soft-link"
	}
	return "rel-other"
}

// roleLabel is a relation's role tag as the picker labels it.
func roleLabel(role schema.Role) string {
	if role == "" {
		return "untagged"
	}
	return string(role)
}

// relationsPanel is the body of the right panel for the entity id. A nil
// component leaves the panel out.
func (s *Server) relationsPanel(id string, tabs Tabs) templ.Component {
	p, err := s.relPanelFor(id, tabs)
	if err != nil {
		return relPanelError(id, err.Error())
	}
	return relationsPanel(p)
}

// relPanelFor reads the panel of id. The draft and the created names are
// read first: the drafts lock comes before the build lock.
func (s *Server) relPanelFor(id string, tabs Tabs) (relPanel, error) {
	p := relPanel{ID: id, Tabs: tabs}
	d, err := s.draft(id)
	if err != nil {
		return p, err
	}
	authored, _, err := d.Entity()
	if err != nil {
		return p, err
	}
	snap := s.snapshot()
	var rows []relRow
	err = s.read(func(cur *loaded) error {
		ctx := s.readContext()
		e, err := cur.res.Entity(ctx, id)
		if err != nil {
			return err
		}
		rows = authoredRows(cur, ctx, authored.Relations, e.Edges, d.Dirty(), snap)
		for _, ed := range e.Edges {
			if ed.Direction == resolve.Out {
				continue
			}
			rows = append(rows, derivedRow(cur, ctx, ed))
		}
		return nil
	})
	if err != nil {
		return p, err
	}
	p.Groups = groupRows(rows)
	return p, nil
}

// authoredRows are the entity's own relations, indexed in its authored list.
// With a draft holding changes every authored relation shows, as the draft
// has it; without one, only those the resolver shows under the worldline.
func authoredRows(cur *loaded, ctx resolve.Context, rels []world.Relation, edges []resolve.Edge, dirty bool, snap snapshot) []relRow {
	shown := make([]bool, len(rels))
	if dirty {
		for i := range shown {
			shown[i] = true
		}
	} else {
		// Match each edge the resolver shows to the first unclaimed authored
		// relation saying the same, so an edit names the right index.
		for _, ed := range edges {
			if ed.Direction != resolve.Out {
				continue
			}
			for i, r := range rels {
				if !shown[i] && r.Type == ed.Relation && r.Target == ed.Target &&
					r.Note == ed.Note && equalPriority(r.Priority, ed.Priority) {
					shown[i] = true
					break
				}
			}
		}
	}
	var out []relRow
	for i, r := range rels {
		if !shown[i] {
			continue
		}
		row := relRow{
			Index: i, Relation: r.Type, Target: r.Target, Note: r.Note,
			Conds: slices.Clone(r.ValidIn),
		}
		if r.Priority != nil {
			row.Priority = strconv.Itoa(*r.Priority)
		}
		var role schema.Role
		if rel, ok := cur.pack.Relation(r.Type); ok {
			role = rel.Role
		}
		row.Class = relClass(role)
		row.Name, row.Linked = targetName(cur, ctx, r.Target, snap)
		out = append(out, row)
	}
	return out
}

// derivedRow is an inverse, symmetric or inbound edge: read only, with where
// it comes from.
func derivedRow(cur *loaded, ctx resolve.Context, ed resolve.Edge) relRow {
	row := relRow{
		Index: -1, Relation: ed.Relation, Target: ed.Target, Note: ed.Note,
		Class: relClass(schema.Role(ed.Role)),
	}
	if ed.Priority != nil {
		row.Priority = strconv.Itoa(*ed.Priority)
	}
	row.Name, row.Linked = targetName(cur, ctx, ed.Target, snapshot{})
	switch fwd, inverse := cur.pack.IsInverseName(ed.Relation); {
	case ed.Direction == resolve.In:
		row.From = "inbound: authored on " + row.Name
	case inverse:
		row.From = "derived: inverse of " + fwd + " on " + row.Name
	default:
		row.From = "derived: symmetric, authored on " + row.Name
	}
	return row
}

// targetName is what the writer sees of a relation's target: its name under
// the worldline, or the typed name of an entity created in an open draft. A
// target that is neither is not shown by name or ID.
func targetName(cur *loaded, ctx resolve.Context, id string, snap snapshot) (string, bool) {
	if e, err := cur.res.Entity(ctx, id); err == nil {
		return e.Name, true
	}
	if c, ok := snap.created[id]; ok {
		return c.Name, true
	}
	return "not present under this worldline", false
}

func equalPriority(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// groupRows sorts rows into the role groups, by the class their role gave
// them: membership, containment, then other. Empty groups are left out.
func groupRows(rows []relRow) []relGroup {
	groups := make([]relGroup, len(relGroupOrder)+1)
	for i, r := range relGroupOrder {
		groups[i].Label = string(r)
	}
	groups[len(relGroupOrder)].Label = otherGroup
	for _, row := range rows {
		g := len(relGroupOrder)
		for i, r := range relGroupOrder {
			if row.Class == relClass(r) {
				g = i
			}
		}
		groups[g].Rows = append(groups[g].Rows, row)
	}
	return slices.DeleteFunc(groups, func(g relGroup) bool { return len(g.Rows) == 0 })
}

// relURL is the address of one of the panel's actions on id, carrying the
// open tabs so the re-rendered panel links within them.
func relURL(id string, tabs Tabs, action string, extra url.Values) string {
	q := url.Values{}
	q.Set(tabsParam, joinIDs(tabs.IDs))
	q.Set("at", tabs.Active)
	for k, vs := range extra {
		q[k] = vs
	}
	return "/entity/" + id + "/relations/" + action + "?" + q.Encode()
}

// rowURL is the address of an edit to the authored relation at index i.
func rowURL(id string, tabs Tabs, i int, edit string) string {
	return relURL(id, tabs, strconv.Itoa(i)+"/"+edit, nil)
}

// relationEdit applies one edit to the authored relation the request names,
// then answers with the re-rendered panel.
func (s *Server) relationEdit(w http.ResponseWriter, r *http.Request, change func(i int) (world.Change, error)) {
	id := r.PathValue("id")
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil || i < 0 {
		badRequest(w, "relation index %q", r.PathValue("i"))
		return
	}
	tabs := s.tabsFrom(r, r.FormValue("at"))
	c, err := change(i)
	if err != nil {
		s.answerPanel(w, r, id, tabs, err)
		return
	}
	err = s.editDraft(id, func(d *Draft) error {
		e, _, err := d.Entity()
		if err != nil {
			return err
		}
		if i >= len(e.Relations) {
			return fmt.Errorf("there is no relation %d", i)
		}
		d.Changes = append(d.Changes, c)
		if _, err := d.Bytes(); err != nil {
			d.Changes = d.Changes[:len(d.Changes)-1]
			return err
		}
		return nil
	})
	s.answerPanel(w, r, id, tabs, err)
}

// answerPanel re-renders the panel after an edit, with err shown in it when
// the edit was refused, and marks the entity dirty when it was not.
func (s *Server) answerPanel(w http.ResponseWriter, r *http.Request, id string, tabs Tabs, editErr error) {
	p, err := s.relPanelFor(id, tabs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if editErr != nil {
		p.Err = editErr.Error()
	} else {
		trigger(w, eventDirty)
	}
	render(w, r, relationsPanel(p))
}

func (s *Server) relationRemove(w http.ResponseWriter, r *http.Request) {
	s.relationEdit(w, r, func(i int) (world.Change, error) {
		return world.Change{Path: []string{"relations", strconv.Itoa(i)}, Delete: true}, nil
	})
}

// relationPriority sets a relation's priority; an empty one deletes the key.
func (s *Server) relationPriority(w http.ResponseWriter, r *http.Request) {
	s.relationEdit(w, r, func(i int) (world.Change, error) {
		path := []string{"relations", strconv.Itoa(i), "priority"}
		v := strings.TrimSpace(r.FormValue("priority"))
		if v == "" {
			return world.Change{Path: path, Delete: true}, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return world.Change{}, fmt.Errorf("priority %q is not a whole number", v)
		}
		return world.Change{Path: path, Value: n}, nil
	})
}

// relationNote sets a relation's note; an empty one deletes the key.
func (s *Server) relationNote(w http.ResponseWriter, r *http.Request) {
	s.relationEdit(w, r, func(i int) (world.Change, error) {
		path := []string{"relations", strconv.Itoa(i), "note"}
		v := strings.TrimSpace(r.FormValue("note"))
		if v == "" {
			return world.Change{Path: path, Delete: true}, nil
		}
		return world.Change{Path: path, Value: v}, nil
	})
}
