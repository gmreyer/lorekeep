package world

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// World is everything authored under one world root, parsed but not yet
// checked against anything.
//
// Entities and Statements keep walk order, which is lexical by directory and
// then by filename. That order is deliberate rather than incidental: the index
// and the snapshot are golden-tested, and an order that varied with the
// filesystem would make every golden diff unreadable.
type World struct {
	Root       string
	Entities   []*Entity
	Statements []*Statement

	byID map[string]lookup
}

type lookup struct {
	kind  Kind
	index int
}

// Entity returns the entity with the given ID.
func (w *World) Entity(id string) (*Entity, bool) {
	l, ok := w.byID[id]
	if !ok || l.kind != KindEntity {
		return nil, false
	}
	return w.Entities[l.index], true
}

// Statement returns the statement with the given ID.
func (w *World) Statement(id string) (*Statement, bool) {
	l, ok := w.byID[id]
	if !ok || l.kind != KindStatement {
		return nil, false
	}
	return w.Statements[l.index], true
}

// KindOf reports what an ID refers to.
//
// The distinction is what lets a finding say "that is a statement, and a
// statement can never be a relation endpoint" rather than "that does not
// exist". They are very different authoring mistakes and deserve very
// different messages.
func (w *World) KindOf(id string) (Kind, bool) {
	l, ok := w.byID[id]
	return l.kind, ok
}

// Load reads every Markdown file under root.
//
// It returns the world it managed to parse alongside every framing and YAML
// fault it found: a file that will not parse is skipped, and the rest still
// load, because a writer should see every mistake in one build rather than one
// per build.
//
// Duplicate IDs are not resolved here. Both documents are kept and the lookups
// resolve to the first in walk order, so that internal/validate can report the
// duplicate with both files named.
func Load(root string) (*World, []Finding) {
	w := &World{Root: root, byID: map[string]lookup{}}
	var findings []Finding

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			findings = append(findings, Finding{
				Code: CodeUnreadable, Severity: SeverityError,
				File: relSlash(root, p), Line: 1,
				Msg: "cannot read: " + err.Error(),
			})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// A build directory is derived output, and a dot-directory is
			// tooling. Neither is authored lore.
			if name := d.Name(); p != root && (name == "build" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}

		name := relSlash(root, p)
		data, err := os.ReadFile(p)
		if err != nil {
			findings = append(findings, Finding{
				Code: CodeUnreadable, Severity: SeverityError, File: name, Line: 1,
				Msg: "cannot read: " + err.Error(),
			})
			return nil
		}

		doc, parsed := Parse(name, data)
		findings = append(findings, parsed...)
		w.add(doc)
		return nil
	})
	if err != nil {
		findings = append(findings, Finding{
			Code: CodeUnreadable, Severity: SeverityError, File: filepath.ToSlash(root), Line: 1,
			Msg: "cannot walk the world directory: " + err.Error(),
		})
	}

	return w, findings
}

func (w *World) add(doc Document) {
	switch {
	case doc.Entity != nil:
		w.Entities = append(w.Entities, doc.Entity)
		w.claim(doc.Entity.ID, KindEntity, len(w.Entities)-1)
	case doc.Statement != nil:
		w.Statements = append(w.Statements, doc.Statement)
		w.claim(doc.Statement.ID, KindStatement, len(w.Statements)-1)
	}
}

// claim registers an ID, first in walk order winning. A later claim on the
// same ID is left alone rather than overwritten, so the lookup is stable and
// the duplicate is still visible in the document lists for the validator.
func (w *World) claim(id string, kind Kind, index int) {
	if id == "" {
		return
	}
	if _, taken := w.byID[id]; taken {
		return
	}
	w.byID[id] = lookup{kind: kind, index: index}
}

// relSlash renders a path relative to the world root with forward slashes, so
// that findings and goldens read the same on Windows and on CI.
func relSlash(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return path.Clean(filepath.ToSlash(rel))
}
