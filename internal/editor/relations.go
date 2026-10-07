package editor

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"gopkg.in/yaml.v3"

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
	s.mux.HandleFunc("GET /entity/{id}/relations/search", s.relationSearch)
	s.mux.HandleFunc("POST /entity/{id}/relations/link", s.relationLink)
	s.mux.HandleFunc("POST /entity/{id}/relations/create", s.relationCreate)
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

// ---- the target-first picker (round 5, option B) ----

// relChoice is one relation the pack allows between two types.
type relChoice struct {
	Name  string
	Class string
	Role  string
}

// validRelations are the relations the pack allows from an entity of type
// src to one of type dst, in pack order. Each relation is declared once, so
// a symmetric one is offered once.
func validRelations(pack *schema.Pack, src, dst string) []relChoice {
	var out []relChoice
	for _, rel := range pack.Relations {
		if pack.AllowsDomain(rel.Name, src) && pack.AllowsRange(rel.Name, dst) {
			out = append(out, relChoice{Name: rel.Name, Class: relClass(rel.Role), Role: roleLabel(rel.Role)})
		}
	}
	return out
}

// targetTypes are the entity types some relation allowed from src can point
// at, in pack order: what a created entity may be.
func targetTypes(pack *schema.Pack, src string) []string {
	var out []string
	for _, t := range pack.Types {
		if len(validRelations(pack, src, t.Name)) > 0 {
			out = append(out, t.Name)
		}
	}
	return out
}

// maxHits caps the picker's search results.
const maxHits = 20

// matchEntities finds the entities whose name or an alias contains q,
// ignoring case: prefix matches first, then the rest, each by name. self is
// never a hit.
func matchEntities(ents []resolve.Summary, self, q string) []resolve.Summary {
	q = strings.ToLower(q)
	type ranked struct {
		e    resolve.Summary
		rank int
	}
	var found []ranked
	for _, e := range ents {
		if e.ID == self {
			continue
		}
		rank := -1
		for _, n := range append([]string{e.Name}, e.Aliases...) {
			n = strings.ToLower(n)
			switch {
			case strings.HasPrefix(n, q):
				rank = 0
			case strings.Contains(n, q) && rank < 0:
				rank = 1
			}
		}
		if rank >= 0 {
			found = append(found, ranked{e, rank})
		}
	}
	slices.SortStableFunc(found, func(a, b ranked) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		if c := strings.Compare(strings.ToLower(a.e.Name), strings.ToLower(b.e.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.e.ID, b.e.ID)
	})
	out := make([]resolve.Summary, 0, min(len(found), maxHits))
	for _, f := range found[:min(len(found), maxHits)] {
		out = append(out, f.e)
	}
	return out
}

// relSearch is the picker's results for one query.
type relSearch struct {
	ID      string
	Tabs    Tabs
	Query   string
	SrcType string
	Hits    []relHit
	// Types are what a created entity may be; one means it is preselected.
	Types []string
}

// relHit is one entity found, with the relations it can be linked by.
type relHit struct {
	ID, Name, Type string
	Choices        []relChoice
}

// relationSearch answers the picker's active search.
func (s *Server) relationSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m := relSearch{ID: id, Tabs: s.tabsFrom(r, r.URL.Query().Get("at")), Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if m.Query == "" {
		render(w, r, templ.NopComponent)
		return
	}
	err := s.read(func(cur *loaded) error {
		ctx := s.readContext()
		src, err := cur.res.Entity(ctx, id)
		if err != nil {
			return err
		}
		ents, err := cur.res.Entities(ctx)
		if err != nil {
			return err
		}
		m.SrcType = src.Type
		for _, e := range matchEntities(ents, id, m.Query) {
			m.Hits = append(m.Hits, relHit{ID: e.ID, Name: e.Name, Type: e.Type,
				Choices: validRelations(cur.pack, src.Type, e.Type)})
		}
		m.Types = targetTypes(cur.pack, src.Type)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, relHits(m))
}

// relAsk is the relation choice for a target with more than one: an
// existing entity (Target set) or one to create (Create set).
type relAsk struct {
	ID      string
	Tabs    Tabs
	Name    string
	Choices []relChoice
	// Target is the existing entity to link; empty when creating.
	Target string
	// Create is the type of the entity to create, with Name its name.
	Create string
}

// URL is where choosing the relation rel posts.
func (a relAsk) URL(rel string) string {
	if a.Create != "" {
		return relURL(a.ID, a.Tabs, "create", url.Values{"name": {a.Name}, "type": {a.Create}, "relation": {rel}})
	}
	return relURL(a.ID, a.Tabs, "link", url.Values{"target": {a.Target}, "relation": {rel}})
}

// askRelation answers a pick with more than one valid relation: the choices
// replace the search results, and nothing is added yet.
func askRelation(w http.ResponseWriter, r *http.Request, a relAsk) {
	w.Header().Set("HX-Retarget", "#lk-rel-hits")
	w.Header().Set("HX-Reswap", "innerHTML")
	render(w, r, relAskView(a))
}

// pickRelation settles which relation a pick adds: the one asked for, which
// must be allowed, or the only allowed one. With several and none asked
// for, it returns "".
func pickRelation(choices []relChoice, asked, src, dst string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no relation between %s and %s", src, dst)
	}
	if asked == "" {
		if len(choices) == 1 {
			return choices[0].Name, nil
		}
		return "", nil
	}
	if !slices.ContainsFunc(choices, func(c relChoice) bool { return c.Name == asked }) {
		return "", fmt.Errorf("%s does not run from %s to %s", asked, src, dst)
	}
	return asked, nil
}

// addRelation appends a relation to the draft, as the struct the file
// format declares, never a map.
func addRelation(d *Draft, rel, target string) error {
	d.Changes = append(d.Changes, world.Change{
		Path:  []string{"relations", "-"},
		Value: world.Relation{Type: rel, Target: target},
	})
	if _, err := d.Bytes(); err != nil {
		d.Changes = d.Changes[:len(d.Changes)-1]
		return err
	}
	return nil
}

// sourceType reads the type of the entity whose panel is acting. An entity
// not present is a request for a page that is not there.
func (s *Server) sourceType(id string) (string, error) {
	var typ string
	err := s.read(func(cur *loaded) error {
		e, err := cur.res.Entity(s.readContext(), id)
		typ = e.Type
		return err
	})
	return typ, err
}

// relationLink links the entity to an existing one picked in the search.
// With exactly one valid relation it is added at once.
func (s *Server) relationLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tabs := s.tabsFrom(r, r.FormValue("at"))
	target := r.FormValue("target")
	src, err := s.sourceType(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var (
		choices []relChoice
		name    string
		dst     string
	)
	err = s.read(func(cur *loaded) error {
		if target == id {
			return errors.New("an entity cannot be linked to itself")
		}
		te, err := cur.res.Entity(s.readContext(), target)
		if err != nil {
			return errors.New("that entity is not present under this worldline")
		}
		dst, name = te.Type, te.Name
		choices = validRelations(cur.pack, src, dst)
		return nil
	})
	var rel string
	if err == nil {
		rel, err = pickRelation(choices, r.FormValue("relation"), src, dst)
	}
	if err == nil && rel == "" {
		askRelation(w, r, relAsk{ID: id, Tabs: tabs, Name: name, Choices: choices, Target: target})
		return
	}
	if err == nil {
		err = s.editDraft(id, func(d *Draft) error { return addRelation(d, rel, target) })
	}
	s.answerPanel(w, r, id, tabs, err)
}

// ---- one-click create (round 5 "Created entity") ----

// idChars are the characters of a created ID's random part.
const idChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// idTries bounds the retries on a taken ID; with 36^6 IDs a second try is
// already rare.
const idTries = 64

// newID makes an opaque ID for a new entity: prefix, underscore, six random
// [a-z0-9] from crypto/rand, retried while taken says it is in use. It is
// never derived from a name.
func newID(prefix string, taken func(string) bool) (string, error) {
	buf := make([]byte, 1)
	for range idTries {
		var b strings.Builder
		b.WriteString(prefix + "_")
		for n := 0; n < 6; {
			if _, err := rand.Read(buf); err != nil {
				return "", err
			}
			// Reject the top of the byte range, so every character is
			// equally likely.
			if int(buf[0]) >= 256/len(idChars)*len(idChars) {
				continue
			}
			b.WriteByte(idChars[int(buf[0])%len(idChars)])
			n++
		}
		if id := b.String(); !taken(id) {
			return id, nil
		}
	}
	return "", fmt.Errorf("no free ID for %s after %d tries", prefix, idTries)
}

// idSet reports an ID as taken when it equals one of ids ignoring case:
// IDs and filenames must not collide case-insensitively on Windows.
func idSet(ids []string) func(string) bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[strings.ToLower(id)] = true
	}
	return func(id string) bool { return set[strings.ToLower(id)] }
}

// typeFolder is the world-relative folder a new entity of typ goes in: the
// one every entity of that type already shares, or else one named for the
// type (Step 6 plan Q2). It reads every entity's file, present under the
// worldline or not; nothing here is shown.
func typeFolder(cur *loaded, typ string) string {
	dir := ""
	for _, e := range cur.idx.Entities {
		if e.Type != typ {
			continue
		}
		d := path.Dir(filepath.ToSlash(e.File))
		if dir == "" {
			dir = d
		} else if d != dir {
			return typ
		}
	}
	if dir == "" {
		return typ
	}
	return dir
}

// createdFrontmatter is a created entity's whole file: its fields in file
// order, and no body. A struct, so yaml keeps the order.
type createdFrontmatter struct {
	ID         string               `yaml:"id"`
	Type       string               `yaml:"type"`
	Name       string               `yaml:"name"`
	Status     world.Status         `yaml:"status"`
	Visibility world.VisibilityKind `yaml:"visibility"`
}

// createdContent is the file of a new draft entity.
func createdContent(id, typ, name string) ([]byte, error) {
	fm, err := yaml.Marshal(createdFrontmatter{
		ID: id, Type: typ, Name: name,
		Status: world.StatusDraft, Visibility: world.VisibilityInternal,
	})
	if err != nil {
		return nil, err
	}
	out := append([]byte("---\n"), fm...)
	return append(out, "---\n"...), nil
}

// relationCreate creates an entity named in the search and links the entity
// to it. Both go into the entity's draft in one edit, so its save writes the
// new file with the link and a link never names a missing file. The new
// entity opens in a background tab.
func (s *Server) relationCreate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tabs := s.tabsFrom(r, r.FormValue("at"))
	name := strings.TrimSpace(r.FormValue("name"))
	typ := r.FormValue("type")
	src, err := s.sourceType(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var choices []relChoice
	err = s.read(func(cur *loaded) error {
		if !slices.Contains(targetTypes(cur.pack, src), typ) {
			return fmt.Errorf("no relation between %s and %s", src, typ)
		}
		choices = validRelations(cur.pack, src, typ)
		return nil
	})
	if err == nil && name == "" {
		err = errors.New("type a name to create")
	}
	var rel string
	if err == nil {
		rel, err = pickRelation(choices, r.FormValue("relation"), src, typ)
	}
	if err == nil && rel == "" {
		askRelation(w, r, relAsk{ID: id, Tabs: tabs, Name: name, Choices: choices, Create: typ})
		return
	}
	var created string
	if err == nil {
		created, err = s.createLinked(id, typ, name, rel)
	}
	if err != nil {
		s.answerPanel(w, r, id, tabs, err)
		return
	}
	tabs = tabs.Background(created)
	strip, err := s.openTabs(w, tabs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.relPanelFor(id, tabs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	trigger(w, eventDirty)
	render(w, r, relCreated(p, strip))
}

// createLinked adds a new entity and the relation to it to the draft of id,
// in one edit, and returns the new ID.
func (s *Server) createLinked(id, typ, name, rel string) (string, error) {
	var created string
	err := s.editDraft(id, func(d *Draft) error {
		// The drafts lock is held here, so every open draft's created IDs
		// are read in place, and two creates cannot pick the same one.
		var ids []string
		for _, od := range s.drafts.m {
			for _, c := range od.Created {
				ids = append(ids, c.ID)
			}
		}
		for _, c := range d.Created {
			ids = append(ids, c.ID)
		}
		var folder string
		err := s.read(func(cur *loaded) error {
			prefix, ok := cur.pack.IDPrefix(typ)
			if !ok {
				return fmt.Errorf("%s is not an entity type", typ)
			}
			// Unique against every ID in the world, present under the
			// worldline or not; nothing read here is shown.
			for _, e := range cur.idx.Entities {
				ids = append(ids, e.ID)
			}
			for _, st := range cur.idx.Statements {
				ids = append(ids, st.ID)
			}
			folder = typeFolder(cur, typ)
			var err error
			created, err = newID(prefix, idSet(ids))
			return err
		})
		if err != nil {
			return err
		}
		file := path.Join(folder, created+".md")
		if _, err := os.Stat(s.worldPath(file)); err == nil {
			return errors.New("the new entity's file already exists on disk; nothing was created")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		content, err := createdContent(created, typ, name)
		if err != nil {
			return err
		}
		if err := addRelation(d, rel, created); err != nil {
			return err
		}
		d.Created = append(d.Created, Created{ID: created, Type: typ, Name: name, File: file, Content: content})
		return nil
	})
	return created, err
}
