package validate

import (
	"fmt"
	"path"
	"strings"

	"github.com/gmreyer/lore-core/internal/schema"
	"github.com/gmreyer/lore-core/internal/world"
)

func (v *validator) checkWarnings() {
	refs := v.references()

	v.warnLifespans()
	v.warnOrphans(refs)
	v.warnCanonOnDraft()
	v.warnUnbelievedStatements()
	v.warnDirectoryMismatch()
}

// references is the set of ids something in canon points at.
//
// The spec says "no inbound edges from any canon entity", and this reads that
// as the question it is really asking: does anything in this world refer to
// this at all? A decision named by a dozen conditions is not disconnected, and
// neither is the subject of a statement, even though neither is the target of
// an edge. Counting only typed edges would warn loudest about exactly the
// entities the story turns on.
//
// Only canon documents contribute, so a draft file cannot quietly keep a
// genuinely orphaned entity looking connected.
func (v *validator) references() map[string]bool {
	refs := map[string]bool{}
	note := func(ids ...string) {
		for _, id := range ids {
			if id != "" {
				refs[id] = true
			}
		}
	}
	noteConditions := func(conds []world.Condition) {
		for _, c := range conds {
			note(c.Decision)
		}
	}

	for _, e := range v.w.Entities {
		if e.Status != world.StatusCanon {
			continue
		}
		for _, r := range e.Relations {
			note(r.Target)
			noteConditions(r.ValidIn)
		}
		for _, b := range e.Beliefs {
			note(b.Statement, b.AcquiredFrom)
			noteConditions(b.ValidIn)
		}
		for _, a := range e.Asserts {
			note(a.Statement, a.To)
			noteConditions(a.ValidIn)
		}
		noteConditions(e.ValidIn)
	}
	for _, s := range v.w.Statements {
		if s.Status != world.StatusCanon {
			continue
		}
		note(s.Subject, s.Object)
		noteConditions(s.ValidIn)
	}
	return refs
}

func (v *validator) warnOrphans(refs map[string]bool) {
	for _, e := range v.w.Entities {
		// Only canon is worth warning about. A draft entity nothing points at
		// yet is a file someone started this morning.
		if e.ID == "" || e.Status != world.StatusCanon || refs[e.ID] {
			continue
		}
		v.warn(e.Source, CodeOrphan, "id",
			"nothing in canon refers to %q: no edge, no statement, and no condition names it",
			e.ID)
	}
}

// warnCanonOnDraft reports canon resting on something still being written.
// Shipping it would ship a reference to a file that may still change shape.
func (v *validator) warnCanonOnDraft() {
	isDraft := func(id string) bool {
		e, ok := v.w.Entity(id)
		return ok && e.Status == world.StatusDraft
	}

	for _, e := range v.w.Entities {
		if e.Status != world.StatusCanon {
			continue
		}
		for i, r := range e.Relations {
			if isDraft(r.Target) {
				v.warn(e.Source, CodeCanonDependsOnDraft, fmt.Sprintf("relations[%d].target", i),
					"canon %q depends on %q, which is still draft", e.ID, r.Target)
			}
		}
	}
	for _, s := range v.w.Statements {
		if s.Status != world.StatusCanon {
			continue
		}
		for _, side := range []struct{ path, id string }{{"subject", s.Subject}, {"object", s.Object}} {
			if isDraft(side.id) {
				v.warn(s.Source, CodeCanonDependsOnDraft, side.path,
					"canon statement %q depends on %q, which is still draft", s.ID, side.id)
			}
		}
	}
}

// warnUnbelievedStatements reports a statement nobody holds a position on.
//
// Reification is expensive, and the discipline that keeps it affordable is
// promoting an edge only when someone needs to be wrong about it. A statement
// with no believer and no asserter is a promotion that never paid for itself,
// and it should probably go back to being a plain edge.
func (v *validator) warnUnbelievedStatements() {
	held := map[string]bool{}
	for _, e := range v.w.Entities {
		for _, b := range e.Beliefs {
			held[b.Statement] = true
		}
		for _, a := range e.Asserts {
			held[a.Statement] = true
		}
	}
	for _, s := range v.w.Statements {
		if s.ID == "" || held[s.ID] {
			continue
		}
		v.warn(s.Source, CodeUnbelievedStatement, "id",
			"nobody believes or asserts %q: a statement earns its cost by being something a "+
				"character can be wrong about, so this may belong back as a plain edge", s.ID)
	}
}

// warnDirectoryMismatch reports a file whose folder disagrees with its type.
//
// Frontmatter is canonical and the directory is a human convention, so this is
// a warning and never an error — but a faction filed under characters/ is how
// a writer loses track of it.
//
// A folder resolves to a type only if it is named after one, exactly or with a
// trailing "s". Anything else — a folder grouping a region, or a project type
// whose plural is irregular — resolves to nothing and is left alone, which is
// better than guessing at English and warning wrongly.
func (v *validator) warnDirectoryMismatch() {
	byFolder := map[string]string{world.StatementType: world.StatementType}
	for _, t := range v.pack.Types {
		byFolder[strings.ToLower(t.Name)] = t.Name
		byFolder[strings.ToLower(t.Name)+"s"] = t.Name
	}
	byFolder[world.StatementType+"s"] = world.StatementType

	check := func(src world.Source, actual string, known bool) {
		if !known || src.File == "" {
			return
		}
		folder := path.Base(path.Dir(src.File))
		expected, resolves := byFolder[strings.ToLower(folder)]
		if !resolves || expected == actual {
			return
		}
		v.warn(src, CodeDirectoryMismatch, "type",
			"this is a %s in %s/, where a %s belongs; frontmatter is canonical, so the graph is "+
				"right and the filing is not", actual, folder, expected)
	}

	for _, e := range v.w.Entities {
		// An entity whose type is not declared has one real problem already,
		// and a folder warning derived from that same unknown type is noise
		// on top of it.
		check(e.Source, e.Type, v.typeIsKnown(e))
	}
	for _, s := range v.w.Statements {
		check(s.Source, world.StatementType, true)
	}
}

// warnLifespans reports a character taking part in an event outside the
// interval they were alive for. It catches a surprising share of real
// continuity errors at near-zero cost.
//
// It binds to the participation role rather than to any relation name. A world
// that calls participation something else gets the check for free, and a
// validator that looked for a name would silently stop working there.
func (v *validator) warnLifespans() {
	participation := map[string]bool{}
	for _, r := range v.pack.RelationsWithRole(schema.RoleParticipation) {
		participation[r.Name] = true
	}
	if len(participation) == 0 {
		return
	}

	for _, e := range v.w.Entities {
		life, ok := v.span(e.Lifespan)
		if !ok {
			continue
		}
		for i, r := range e.Relations {
			if !participation[r.Type] {
				continue
			}
			event, found := v.w.Entity(r.Target)
			if !found {
				continue
			}
			when, ok := v.span(event.Date)
			if !ok {
				continue
			}
			if reason, outside := life.excludes(when); outside {
				v.warn(e.Source, CodeLifespanMismatch, fmt.Sprintf("relations[%d].target", i),
					"%q took part in %q, which happened %s: %s is %s, the event is %s",
					e.ID, r.Target, reason, e.ID, life, when)
			}
		}
	}
}
