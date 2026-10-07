package editor

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/world"
)

// The entity page (round 2): the name, the type, the fields line, the prose
// as an editable Markdown field, and for an agent what it believes.

func (s *Server) entityRoutes() {
	s.mux.HandleFunc("GET /entity/{id}", s.entityPage)
	s.mux.HandleFunc("POST /entity/{id}/body", s.bodyEdit)
}

// agentGroup is the core pack's group of types that hold beliefs.
const agentGroup = "agent"

// entityView is what the entity page shows: the entity as its draft leaves
// it, or as its file has it when no draft is open.
type entityView struct {
	ID, Name, Type string
	Body           string
	Fields         fieldsData
	// Believes is nil for an entity that holds no beliefs.
	Believes *beliefBlock
}

// beliefBlock is "What <name> believes".
type beliefBlock struct {
	Name  string
	Lines []beliefLine
}

type beliefLine struct {
	Sentence string
	// Source is "own" or "from <faction>".
	Source string
	// Contradicts marks a belief that disagrees with canon.
	Contradicts bool
}

// createdView is the page of an entity made in a draft and not yet saved.
type createdView struct {
	Name, Type, Origin string
}

// entityPage is the entity in the shell, as the active tab.
func (s *Server) entityPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tabs := s.tabsFrom(r, id)

	if c, origin, ok := s.created(id); ok {
		cv := createdView{Name: c.Name, Type: c.Type}
		err := s.read(func(cur *loaded) error {
			if e, err := cur.res.Entity(s.readContext(), origin); err == nil {
				cv.Origin = e.Name
			}
			return nil
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p, err := s.page(tabs, c.Name, createdPage(cv), nil)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		render(w, r, layout(p))
		return
	}

	v, err := s.entityView(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.page(tabs, v.Name, entityPageView(v), s.relationsPanel(id, tabs))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, layout(p))
}

func (s *Server) entityView(id string) (entityView, error) {
	fd, err := s.fieldsData(id)
	if err != nil {
		return entityView{}, err
	}
	d, err := s.draft(id)
	if err != nil {
		return entityView{}, err
	}
	ent, _, err := d.Entity()
	if err != nil {
		return entityView{}, err
	}
	data, err := d.Bytes()
	if err != nil {
		return entityView{}, err
	}
	body, err := world.RawBody(data)
	if err != nil {
		return entityView{}, err
	}
	v := entityView{ID: id, Name: ent.Name, Type: ent.Type, Body: body, Fields: fd}
	err = s.read(func(cur *loaded) error {
		if agents, _ := cur.pack.ExpandGroup(agentGroup); slices.Contains(agents, ent.Type) {
			v.Believes = s.beliefs(cur, id, ent.Name)
		}
		return nil
	})
	return v, err
}

// beliefs reads what an agent believes, through the resolver. The editor's
// context is omniscient, so an entity's statements are canon: a belief that
// holds the other value is marked.
func (s *Server) beliefs(cur *loaded, id, name string) *beliefBlock {
	ctx := s.readContext()
	bs, err := cur.res.Beliefs(ctx, id)
	if err != nil {
		return &beliefBlock{Name: name}
	}
	nameOf := func(id string) string {
		if e, err := cur.res.Entity(ctx, id); err == nil {
			return e.Name
		}
		return "something unknown"
	}
	canon := map[string]map[string]string{} // subject -> statement -> canon Held
	canonOf := func(b resolve.Belief) string {
		m, ok := canon[b.Subject]
		if !ok {
			m = map[string]string{}
			if e, err := cur.res.Entity(ctx, b.Subject); err == nil {
				for _, st := range e.Statements {
					m[st.Statement] = st.Held
				}
			}
			canon[b.Subject] = m
		}
		return m[b.Statement]
	}
	out := &beliefBlock{Name: name}
	for _, b := range bs {
		line := beliefLine{
			Sentence: nameOf(b.Subject) + " " + strings.ReplaceAll(b.Relation, "_", " ") + " " + nameOf(b.Object),
			Source:   "own",
		}
		if b.Held == string(world.TruthFalse) {
			line.Sentence += " is false"
		}
		if b.Via != "" {
			line.Source = "from " + nameOf(b.Via)
		}
		truth := canonOf(b)
		held := b.Held == string(world.TruthTrue) || b.Held == string(world.TruthFalse)
		known := truth == string(world.TruthTrue) || truth == string(world.TruthFalse)
		line.Contradicts = held && known && b.Held != truth
		out.Lines = append(out.Lines, line)
	}
	return out
}

// bodyEdit puts the textarea's prose in the entity's draft; nothing is
// rendered back. A body equal to the saved one leaves the draft's prose
// unset.
func (s *Server) bodyEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		badRequest(w, "%v", err)
		return
	}
	body := strings.ReplaceAll(r.PostForm.Get("body"), "\r\n", "\n")
	err := s.editDraft(id, func(d *Draft) error {
		_, changed, err := world.ReplaceBody(d.Base, body)
		if err != nil {
			return err
		}
		if changed {
			d.Body = &body
		} else {
			d.Body = nil
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	trigger(w, eventDirty)
}
