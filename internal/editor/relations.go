package editor

import "github.com/a-h/templ"

// Task 3 owns this file and relations.templ: the relations panel, the
// target-first picker and one-click create (round 5).

func (s *Server) relationRoutes() {}

// relationsPanel is the body of the right panel for the entity id. A nil
// component leaves the panel out.
func (s *Server) relationsPanel(id string, tabs Tabs) templ.Component {
	return templ.NopComponent
}
