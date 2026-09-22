package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gmreyer/lore-core/internal/world"
)

// checkDuplicateEdges enforces that one fact is authored once.
//
// Two shapes break it. The same edge twice on one entity, under the same
// conditions, would be stored, counted, and traversed twice; the same edge
// under different conditions is legal, because that is how one entity says
// "in this worldline or that one". And a symmetric edge authored on both
// endpoints is the symmetric form of authoring an inverse: the relation reads
// the same from either end, so the second copy is the first one restated.
//
// A mirror is an error whatever its conditions. Every branch of one symmetric
// fact lives in one file, so a reader finds the whole relationship in one
// place. Either endpoint may hold it; there is no canonical side.
//
// Findings land on the later copy in walk order and name the earlier one, so
// the writer knows which line to delete and where the fact already lives.
// Whether a relation is symmetric comes from the pack. An undeclared relation
// is skipped: it already has its own finding, and has no symmetry to check.
func (v *validator) checkDuplicateEdges() {
	type site struct {
		file  string
		index int
	}
	type pair struct{ rel, from, to string }

	authored := map[string]site{} // exact edges: source, relation, target, conditions
	symmetric := map[pair]site{}  // symmetric edges by direction, conditions ignored

	for _, e := range v.w.Entities {
		if e.ID == "" {
			continue
		}
		for i, r := range e.Relations {
			rel, ok := v.pack.Relation(r.Type)
			if !ok || r.Target == "" {
				continue
			}
			here := site{file: e.Source.File, index: i}
			path := fmt.Sprintf("relations[%d]", i)

			key := strings.Join([]string{e.ID, r.Type, r.Target, conditionKey(r.ValidIn)}, "\x00")
			if first, seen := authored[key]; seen {
				v.err(e.Source, CodeDuplicateEdge, path,
					"%q %s %q is already authored at relations[%d] of this file, under the same "+
						"conditions; one fact is authored once", e.ID, r.Type, r.Target, first.index)
				continue
			}
			authored[key] = here

			if !rel.Symmetric || r.Target == e.ID {
				continue
			}
			if first, seen := symmetric[pair{r.Type, r.Target, e.ID}]; seen && first.file != here.file {
				v.err(e.Source, CodeSymmetricMirror, path,
					"%q %s %q is already authored in %s, and %s reads the same from either end; "+
						"keep it in one of the two files, with every valid_in branch of it",
					e.ID, r.Type, r.Target, first.file, r.Type)
				continue
			}
			if _, seen := symmetric[pair{r.Type, e.ID, r.Target}]; !seen {
				symmetric[pair{r.Type, e.ID, r.Target}] = here
			}
		}
	}
}

// conditionKey renders a valid_in list as a key that ignores order, since a
// list of conditions is a set.
func conditionKey(conds []world.Condition) string {
	parts := make([]string, len(conds))
	for i, c := range conds {
		parts[i] = c.Decision + "=" + c.Outcome
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}
