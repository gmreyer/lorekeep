package validate

import (
	"fmt"

	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

// Validate checks a loaded world against a merged schema pack and returns
// every fault it found, errors and warnings together, in a stable order.
//
// It collects rather than stopping at the first fault: a writer should fix a
// world in one sitting rather than in one build per mistake. Checks suppress
// the ones they would make meaningless — an edge whose relation is not
// declared has no domain to violate — so a single mistake produces a single
// finding rather than a cascade.
func Validate(w *world.World, pack *schema.Pack) world.Findings {
	v := &validator{w: w, pack: pack}

	v.checkIdentity()
	v.checkEntities()
	v.checkDuplicateEdges()
	v.checkStatements()
	v.checkWarnings()

	v.findings.Sort()
	return v.findings
}

type validator struct {
	w        *world.World
	pack     *schema.Pack
	findings world.Findings

	// knownType caches whether an entity's declared type is in the pack, so
	// that a check depending on it can be skipped rather than piled on top of
	// the unknown-type finding.
	knownType map[string]bool
}

func (v *validator) err(src world.Source, code world.Code, path, format string, args ...any) {
	v.emit(world.SeverityError, src, code, path, format, args...)
}

func (v *validator) warn(src world.Source, code world.Code, path, format string, args ...any) {
	v.emit(world.SeverityWarning, src, code, path, format, args...)
}

func (v *validator) emit(sev world.Severity, src world.Source, code world.Code, path, format string, args ...any) {
	v.findings = append(v.findings, world.Finding{
		Code:     code,
		Severity: sev,
		File:     src.File,
		Line:     src.LineOf(path),
		Path:     path,
		Msg:      fmt.Sprintf(format, args...),
	})
}

// typeIsKnown reports whether an entity's declared type is in the merged pack,
// caching the answer.
func (v *validator) typeIsKnown(e *world.Entity) bool {
	if v.knownType == nil {
		v.knownType = map[string]bool{}
	}
	known, seen := v.knownType[e.ID]
	if !seen {
		known = e.Type != "" && v.pack.HasType(e.Type)
		v.knownType[e.ID] = known
	}
	return known
}
