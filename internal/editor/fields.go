package editor

import (
	"github.com/a-h/templ"

	"github.com/gmreyer/lorekeep/internal/world"
)

// Task 2 owns this file and fields.templ: the inline fields form (round 4),
// and the "only when" chips every valid_in is edited with.

func (s *Server) fieldRoutes() {}

// CondTarget is one valid_in list to edit with "only when" chips: the
// entity's own, a relation's, or a belief's (round 4).
type CondTarget struct {
	// Entity is the entity whose draft holds the list.
	Entity string
	// Path is the list's world.Change path in that file: {"valid_in"},
	// {"relations", "2", "valid_in"}, {"beliefs", "0", "valid_in"}.
	Path []string
	// Conds are the conditions as the draft has them.
	Conds []world.Condition
}

// conditionChips renders a CondTarget's chips with "+ only when…", and posts
// its edits to the entity's draft. Task 3 uses it on each relation row.
func conditionChips(t CondTarget) templ.Component {
	return templ.NopComponent
}
