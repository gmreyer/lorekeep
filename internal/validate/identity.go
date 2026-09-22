package validate

import (
	"regexp"
	"strings"

	"github.com/gmreyer/lore-core/internal/world"
)

// idPattern is the permitted shape of an identifier: lowercase letters,
// digits, and underscores.
//
// The constraint is narrow on purpose. IDs appear in filenames, in URLs, in
// save files, and in SQL, and every character outside this set is a place one
// of those could disagree with another. A lowercased ULID fits, so a project
// that wants identifiers with no semantics at all still has them.
var idPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// document is an entity or a statement seen as what the identity rules care
// about: an ID, a kind, and a file.
type document struct {
	id   string
	kind world.Kind
	src  world.Source
}

func (v *validator) documents() []document {
	docs := make([]document, 0, len(v.w.Entities)+len(v.w.Statements))
	for _, e := range v.w.Entities {
		docs = append(docs, document{id: e.ID, kind: world.KindEntity, src: e.Source})
	}
	for _, s := range v.w.Statements {
		docs = append(docs, document{id: s.ID, kind: world.KindStatement, src: s.Source})
	}
	return docs
}

// checkIdentity enforces the rules that hold across the whole world at once:
// the ID charset, uniqueness, and the two case-collision rules.
//
// Entities and statements share one ID namespace. A statement is not an entity
// type, but it is an identifier, and a world where stmt_x and char_x could
// both be "x" would have two things answering to one name in the index, in a
// save file, and in a URL.
func (v *validator) checkIdentity() {
	docs := v.documents()

	byID := make(map[string]document, len(docs))
	byFoldedID := make(map[string]document, len(docs))
	byFoldedFile := make(map[string]document, len(docs))

	for _, d := range docs {
		if d.id == "" {
			v.err(d.src, CodeMissingField, "id",
				"no id: every document carries a stable, opaque id that nothing renames")
		} else {
			v.checkIDShape(d)
			v.claimID(d, byID, byFoldedID)
		}
		v.claimFile(d, byFoldedFile)
	}
}

func (v *validator) checkIDShape(d document) {
	if idPattern.MatchString(d.id) {
		return
	}
	v.err(d.src, CodeBadID, "id",
		"id %q is not lowercase letters, digits, and underscores; ids travel into filenames, "+
			"urls, save files, and sql, and anything else invites one of those to disagree "+
			"with another", d.id)
}

func (v *validator) claimID(d document, byID, byFolded map[string]document) {
	if prev, taken := byID[d.id]; taken {
		v.err(d.src, CodeDuplicateID, "id",
			"id %q is already used by %s", d.id, prev.src.File)
		return
	}
	folded := strings.ToLower(d.id)
	if prev, taken := byFolded[folded]; taken {
		v.err(d.src, CodeCaseCollision, "id",
			"id %q collides with %q in %s when case is ignored",
			d.id, prev.id, prev.src.File)
		return
	}
	byID[d.id] = d
	byFolded[folded] = d
}

// claimFile reports two filenames that differ only in case.
//
// Windows treats them as one file and git does not, so a pair that works for
// one writer collides for another — and the writer it breaks for sees a
// checkout that is missing a file, with nothing to explain why.
func (v *validator) claimFile(d document, byFolded map[string]document) {
	if d.src.File == "" {
		return
	}
	folded := strings.ToLower(d.src.File)
	prev, taken := byFolded[folded]
	if !taken {
		byFolded[folded] = d
		return
	}
	if prev.src.File == d.src.File {
		return // the same file, seen twice; not this rule's business
	}
	v.err(d.src, CodeFileCaseCollision, "",
		"filename %q collides with %q when case is ignored; windows treats them as one file "+
			"and git does not, so only one of them survives a fresh clone",
		d.src.File, prev.src.File)
}
