package validate

import (
	"fmt"

	"github.com/gmreyer/lore-core/internal/schema"
	"github.com/gmreyer/lore-core/internal/world"
)

func (v *validator) checkEntities() {
	for _, e := range v.w.Entities {
		v.checkEntityFields(e)
		v.checkEntityEdges(e)
		v.checkBeliefs(e)
		v.checkAssertions(e)
		v.checkConditions(e.Source, "valid_in", e.ValidIn)
	}
}

func (v *validator) checkEntityFields(e *world.Entity) {
	switch {
	case e.Type == "":
		v.err(e.Source, CodeMissingField, "type",
			"no type: the type in frontmatter is what this document is, whatever directory it sits in")
	case !v.pack.HasType(e.Type):
		v.err(e.Source, CodeUnknownEntityType, "type",
			"entity type %q is not declared by the schema pack; the type vocabulary is closed "+
				"and extending it is a schema change", e.Type)
	}
	if e.Name == "" {
		v.err(e.Source, CodeMissingField, "name",
			"no name: an id is opaque, so the name is what a writer and an editor see")
	}

	v.checkStatus(e.Source, e.Status)
	v.checkVisibility(e.Source, e.Visibility)

	v.checkInterval(e.Source, "lifespan", e.Lifespan)
	v.checkInterval(e.Source, "date", e.Date)

	if v.typeIsKnown(e) {
		v.checkFieldPlacement(e)
	}
	v.checkOutcomes(e)
}

// checkFieldPlacement reports a field authored on a type it does not belong
// to. It runs only once the type itself is known, so an unknown type produces
// one finding rather than one per field.
func (v *validator) checkFieldPlacement(e *world.Entity) {
	for _, f := range []struct {
		path    string
		present bool
		owner   string
		why     string
	}{
		{"lifespan", e.Lifespan != nil, schema.TypeCharacter, "a lifespan is the interval between a birth and a death"},
		{"date", e.Date != nil, schema.TypeEvent, "a date is when something happened"},
		{"outcomes", e.Outcomes != nil, schema.TypeDecision, "outcomes are the branches a decision opens"},
	} {
		if f.present && e.Type != f.owner {
			v.err(e.Source, CodeFieldOnWrongType, f.path,
				"%s belongs to a %s, not to a %s: %s", f.path, f.owner, e.Type, f.why)
		}
	}
}

// checkOutcomes validates a decision's declared branches.
//
// Outcome keys obey the same charset as ids, because every valid_in in the
// world names one and they end up in save files exactly as ids do.
func (v *validator) checkOutcomes(e *world.Entity) {
	if e.Type == schema.TypeDecision && len(e.Outcomes) == 0 {
		v.err(e.Source, CodeMissingField, "outcomes",
			"a decision declares its outcomes: they are the closed set every valid_in in the "+
				"world may name")
		return
	}

	seen := map[string]bool{}
	for i, out := range e.Outcomes {
		path := fmt.Sprintf("outcomes[%d]", i)
		switch {
		case out == "":
			v.err(e.Source, CodeMissingField, path, "an outcome cannot be empty")
		case !idPattern.MatchString(out):
			v.err(e.Source, CodeBadID, path,
				"outcome %q is not lowercase letters, digits, and underscores; outcomes are "+
					"named in conditions and stored in save files, exactly as ids are", out)
		case seen[out]:
			v.err(e.Source, CodeDuplicateID, path,
				"outcome %q is declared twice", out)
		default:
			seen[out] = true
		}
	}
}

func (v *validator) checkStatus(src world.Source, s world.Status) {
	switch {
	case s == "":
		v.err(src, CodeMissingField, "status",
			"no status: draft, canon, deprecated, or non_canon. There is no default, because a "+
				"default would turn an omission into a claim about canon")
	case !s.Valid():
		v.err(src, CodeBadEnum, "status",
			"status %q is not one of draft, canon, deprecated, non_canon", s)
	}
}

// checkVisibility validates the player-facing axis and resolves a spoiler act
// against the project pack's ordered act list.
func (v *validator) checkVisibility(src world.Source, vis world.Visibility) {
	switch {
	case vis.Raw == "":
		v.err(src, CodeMissingField, "visibility",
			"no visibility: public, internal, or spoiler:<act>. It is not derivable from anything "+
				"else — a fact can be true, known to the speaker, and still a spoiler")
	case !vis.Valid():
		v.err(src, CodeBadEnum, "visibility",
			"visibility %q is not public, internal, or spoiler:<act>", vis.Raw)
	case vis.Kind == world.VisibilitySpoiler:
		if _, ok := v.pack.ActOrdinal(vis.Act); !ok {
			v.err(src, CodeUnknownAct, "visibility",
				"spoiler act %q is not declared by the schema pack; known acts, in order: %s",
				vis.Act, listActs(v.pack))
		}
	}
}

// checkInterval validates an era reference and a precision. Both may be
// absent: an interval that says nothing is a legitimate interval, and most of
// what this system answers is ordering rather than dates.
func (v *validator) checkInterval(src world.Source, path string, iv *world.Interval) {
	if iv == nil {
		return
	}
	if iv.Era != "" {
		if _, ok := v.pack.EraOrdinal(iv.Era); !ok {
			v.err(src, CodeUnknownEra, path+".era",
				"era %q is not declared by the schema pack; known eras, in order: %s",
				iv.Era, listEras(v.pack))
		}
	}
	if !iv.Precision.Valid() {
		v.err(src, CodeBadEnum, path+".precision",
			"precision %q is not exact or approximate", iv.Precision)
	}
}

func listEras(p *schema.Pack) string {
	names := make([]string, 0, len(p.Eras))
	for _, e := range p.Eras {
		names = append(names, e.Key)
	}
	return join(names)
}

func listActs(p *schema.Pack) string {
	names := make([]string, 0, len(p.Acts))
	for _, a := range p.Acts {
		names = append(names, a.Key)
	}
	return join(names)
}

func join(names []string) string {
	if len(names) == 0 {
		return "(none declared)"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
