package editor

import (
	"net/http"

	"github.com/gmreyer/lorekeep/internal/resolve"
)

// Task 2 owns this file and entity.templ: the entity page (round 2).

func (s *Server) entityRoutes() {
	s.mux.HandleFunc("GET /entity/{id}", s.entityPage)
}

// entityPage is the entity in the shell, as the active tab. Until Task 2 it
// shows the name, the type and the prose, read only.
func (s *Server) entityPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tabs := s.tabsFrom(r, id)
	var e resolve.Entity
	err := s.read(func(cur *loaded) error {
		var err error
		e, err = cur.res.Entity(s.readContext(), id)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.page(tabs, e.Name, entityStub(e), s.relationsPanel(id, tabs))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, layout(p))
}
