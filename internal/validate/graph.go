package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

// checkEntityEdges validates every authored relation on an entity: that the
// relation is declared, that it was not authored as an inverse, and that both
// ends satisfy the declared domain and range.
func (v *validator) checkEntityEdges(e *world.Entity) {
	sourceType := v.declaredTypeOf(e)

	for i, r := range e.Relations {
		base := fmt.Sprintf("relations[%d]", i)
		v.checkConditions(e.Source, base+".valid_in", r.ValidIn)

		rel, ok := v.checkRelationName(e.Source, base+".type", r.Type)
		if !ok {
			continue
		}
		if sourceType != "" && !v.pack.AllowsDomain(rel.Name, sourceType) {
			v.err(e.Source, CodeDomainViolation, base+".type",
				"relation %q runs %s, so a %s may not stand at its left-hand end",
				rel.Name, signature(rel), sourceType)
		}

		target, ok := v.resolveEntity(e.Source, base+".target", r.Target)
		if !ok {
			continue
		}
		if targetType := v.declaredTypeOf(target); targetType != "" &&
			!v.pack.AllowsRange(rel.Name, targetType) {
			v.err(e.Source, CodeRangeViolation, base+".target",
				"relation %q runs %s, so it may not point at %q, which is a %s",
				rel.Name, signature(rel), r.Target, targetType)
		}
	}
}

func (v *validator) checkStatements() {
	for _, s := range v.w.Statements {
		v.checkStatementFields(s)
		v.checkStatementTriple(s)
		v.checkConditions(s.Source, "valid_in", s.ValidIn)
	}
}

func (v *validator) checkStatementFields(s *world.Statement) {
	switch {
	case s.Truth == "":
		v.err(s.Source, CodeMissingField, "truth",
			"no truth: a statement exists to carry one, and it is true, false, or unresolved")
	case !s.Truth.Valid():
		v.err(s.Source, CodeBadEnum, "truth",
			"truth %q is not true, false, or unresolved", s.Truth)
	}
	v.checkStatus(s.Source, s.Status)
	v.checkVisibility(s.Source, s.Visibility)
}

// checkStatementTriple validates a statement exactly as it validates an
// authored edge.
//
// That is the point of reification: a statement is an edge someone can be
// wrong about, so it obeys the same closed vocabulary. A triple that could not
// have been written as an edge is a statement about nothing.
func (v *validator) checkStatementTriple(s *world.Statement) {
	rel, relOK := v.checkRelationName(s.Source, "relation", s.Relation)

	subject, subjectOK := v.resolveEntity(s.Source, "subject", s.Subject)
	object, objectOK := v.resolveEntity(s.Source, "object", s.Object)
	if !relOK {
		return
	}

	if subjectOK {
		if t := v.declaredTypeOf(subject); t != "" && !v.pack.AllowsDomain(rel.Name, t) {
			v.err(s.Source, CodeDomainViolation, "subject",
				"relation %q runs %s, so %q, which is a %s, may not stand at its left-hand end",
				rel.Name, signature(rel), s.Subject, t)
		}
	}
	if objectOK {
		if t := v.declaredTypeOf(object); t != "" && !v.pack.AllowsRange(rel.Name, t) {
			v.err(s.Source, CodeRangeViolation, "object",
				"relation %q runs %s, so it may not point at %q, which is a %s",
				rel.Name, signature(rel), s.Object, t)
		}
	}
}

// checkRelationName resolves a relation name against the pack.
//
// An authored inverse gets its own finding rather than "unknown relation",
// because it is a different mistake with a different fix: inverses are
// declared once and derived by the index, so what the author wants is the
// forward relation, on the other entity, pointing back.
func (v *validator) checkRelationName(src world.Source, path, name string) (*schema.Relation, bool) {
	if name == "" {
		v.err(src, CodeMissingField, path, "no relation type")
		return nil, false
	}
	if forward, isInverse := v.pack.IsInverseName(name); isInverse {
		v.err(src, CodeInverseAuthored, path,
			"%q is the derived inverse of %q and is never authored; put %q on the other entity, "+
				"pointing back at this one", name, forward, forward)
		return nil, false
	}
	rel, ok := v.pack.Relation(name)
	if !ok {
		v.err(src, CodeUnknownRelation, path,
			"relation type %q is not declared by the schema pack; the vocabulary is closed, and "+
				"adding a relation is a schema change reviewed like any other", name)
		return nil, false
	}
	return rel, true
}

// resolveEntity looks an id up where an entity is required, and reports the
// three ways that fails.
//
// "Does not exist", "is a statement", and "is the wrong kind" are different
// authoring mistakes — a typo, a misunderstanding of the model, a misreading
// of the field — and they get different findings rather than one vague one.
func (v *validator) resolveEntity(src world.Source, path, id string) (*world.Entity, bool) {
	if id == "" {
		v.err(src, CodeMissingField, path, "no target")
		return nil, false
	}
	kind, found := v.w.KindOf(id)
	if !found {
		v.err(src, CodeDangling, path, "%q does not exist in this world", id)
		return nil, false
	}
	if kind == world.KindStatement {
		v.err(src, CodeStatementEndpoint, path,
			"%q is a statement, and a statement may never stand at either end of a relation; "+
				"a statement is a reified edge, not something edges point at", id)
		return nil, false
	}
	e, _ := v.w.Entity(id)
	return e, true
}

func (v *validator) checkBeliefs(e *world.Entity) {
	for i, b := range e.Beliefs {
		base := fmt.Sprintf("beliefs[%d]", i)
		v.checkStatementReference(e.Source, base+".statement", b.Statement)
		if b.Value == nil {
			v.err(e.Source, CodeMissingField, base+".value",
				"no value: a belief says what the agent takes the statement to be, and leaving it "+
					"implicit would make the edit that flips it invisible in a diff")
		}
		if !b.Confidence.Valid() {
			v.err(e.Source, CodeBadEnum, base+".confidence",
				"confidence %q is not high, medium, or low", b.Confidence)
		}
		v.checkInterval(e.Source, base+".since", b.Since)
		if b.AcquiredFrom != "" {
			v.resolveEntity(e.Source, base+".acquired_from", b.AcquiredFrom)
		}
		v.checkConditions(e.Source, base+".valid_in", b.ValidIn)
	}
}

func (v *validator) checkAssertions(e *world.Entity) {
	for i, a := range e.Asserts {
		base := fmt.Sprintf("asserts[%d]", i)
		v.checkStatementReference(e.Source, base+".statement", a.Statement)
		if a.Value == nil {
			v.err(e.Source, CodeMissingField, base+".value",
				"no value: an assertion says what the agent claims, and comparing it with their "+
					"belief is what makes a lie visible")
		}
		if a.To != "" {
			v.resolveEntity(e.Source, base+".to", a.To)
		}
		v.checkInterval(e.Source, base+".when", a.When)
		v.checkConditions(e.Source, base+".valid_in", a.ValidIn)
	}
}

func (v *validator) checkStatementReference(src world.Source, path, id string) {
	if id == "" {
		v.err(src, CodeMissingField, path, "no statement")
		return
	}
	kind, found := v.w.KindOf(id)
	if !found {
		v.err(src, CodeDangling, path, "statement %q does not exist in this world", id)
		return
	}
	if kind != world.KindStatement {
		v.err(src, CodeWrongKind, path,
			"%q is an entity, not a statement; beliefs are held about reified statements, and a "+
				"plain edge is not something anyone can be wrong about", id)
	}
}

// checkConditions resolves a valid_in list.
//
// Every pair must name a decision that exists and an outcome that decision
// declares. A condition naming an outcome that no longer exists is the failure
// mode this mechanism invites, and catching it here is what keeps branch
// conditions from rotting silently as a story is rewritten.
func (v *validator) checkConditions(src world.Source, base string, conds []world.Condition) {
	for i, c := range conds {
		path := fmt.Sprintf("%s[%d]", base, i)

		if c.Decision == "" {
			v.err(src, CodeMissingField, path+".decision", "a condition names a decision")
			continue
		}
		kind, found := v.w.KindOf(c.Decision)
		if !found {
			v.err(src, CodeDangling, path+".decision",
				"decision %q does not exist in this world", c.Decision)
			continue
		}
		if kind != world.KindEntity {
			v.err(src, CodeWrongKind, path+".decision",
				"%q is a statement, not a decision", c.Decision)
			continue
		}
		decision, _ := v.w.Entity(c.Decision)
		if decision.Type != schema.TypeDecision {
			v.err(src, CodeWrongKind, path+".decision",
				"%q is a %s, not a decision; only a decision declares the outcomes a condition "+
					"may name", c.Decision, decision.Type)
			continue
		}

		if c.Outcome == "" {
			v.err(src, CodeMissingField, path+".outcome", "a condition names an outcome")
			continue
		}
		if !slices.Contains(decision.Outcomes, c.Outcome) {
			v.err(src, CodeUnknownOutcome, path+".outcome",
				"decision %q declares no outcome %q; it declares: %s",
				c.Decision, c.Outcome, join(decision.Outcomes))
		}
	}
}

// declaredTypeOf returns an entity's type only when the pack declares it, so a
// check that depends on the type is skipped rather than layered on top of the
// unknown-type finding.
func (v *validator) declaredTypeOf(e *world.Entity) string {
	if e == nil || !v.typeIsKnown(e) {
		return ""
	}
	return e.Type
}

// signature renders a relation as domain -> range, so a message says what the
// rule is rather than only that it was broken.
func signature(r *schema.Relation) string {
	return strings.Join(r.Domain, "|") + " -> " + strings.Join(r.Range, "|")
}
