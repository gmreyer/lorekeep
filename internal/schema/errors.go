package schema

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Code identifies a class of schema fault. Callers and tests match on the code
// rather than on message text, so wording stays free to improve.
type Code string

const (
	// Loading a pack.
	CodeNotAPack     Code = "not_a_pack"
	CodeParse        Code = "parse"
	CodeUnknownField Code = "unknown_field"
	CodeMissingField Code = "missing_field"
	CodeBadVersion   Code = "bad_version"

	// Names, within a pack and across the merge.
	CodeDuplicateName Code = "duplicate_name"
	CodeCaseCollision Code = "case_collision"
	CodeCollision     Code = "collision"

	// Relation declarations.
	CodeSymmetricInverse Code = "symmetric_inverse"
	CodeSymmetricDomain  Code = "symmetric_domain"
	CodeSelfInverse      Code = "self_inverse"

	// Cross-references, resolvable only once the packs are merged.
	CodeUnknownType Code = "unknown_type"
	CodeUnknownRole Code = "unknown_role"
	CodeGroupCycle  Code = "group_cycle"

	// Tier rules: what belongs in which pack.
	CodeRoleInProject Code = "role_in_project"
	CodeEraInCore     Code = "era_in_core"

	// The core version pin.
	CodeCoreTooOld Code = "core_too_old"
	CodeCoreMajor  Code = "core_major"
	CodeCoreNewer  Code = "core_newer" // a notice, never an error
)

// Source labels which tier a pack came from. It is a label rather than a path,
// so error text does not vary with the path separator.
type Source string

const (
	SourceCore    Source = "core"
	SourceProject Source = "project"
	SourceMerged  Source = "merged"
)

// Error is a single schema fault, located as precisely as the input allows.
type Error struct {
	Code   Code
	Source Source
	File   string // base name, e.g. "relations.yaml"
	Path   string // field path, e.g. "relations[3].domain"
	Msg    string
}

func (e Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Source))
	if e.File != "" {
		b.WriteString("/")
		b.WriteString(e.File)
	}
	if e.Path != "" {
		b.WriteString(": ")
		b.WriteString(e.Path)
	}
	fmt.Fprintf(&b, ": %s [%s]", e.Msg, e.Code)
	return b.String()
}

// Errors is every fault found in one pass. Loading and merging collect rather
// than stop at the first, so a writer fixes a pack in one sitting instead of
// one build per mistake.
type Errors []Error

func (es Errors) Error() string {
	if len(es) == 0 {
		return "no schema errors"
	}
	lines := make([]string, 0, len(es)+1)
	lines = append(lines, fmt.Sprintf("%d schema error(s):", len(es)))
	for _, e := range es {
		lines = append(lines, "  "+e.Error())
	}
	return strings.Join(lines, "\n")
}

// Notice is something worth saying that does not stop the build. The core
// version pin uses one: a project authored against an older core still loads.
type Notice struct {
	Code Code
	Msg  string
}

// errList accumulates faults during a load or a merge.
type errList struct {
	source Source
	errs   Errors
}

func (l *errList) add(code Code, file, path, format string, args ...any) {
	l.errs = append(l.errs, Error{
		Code:   code,
		Source: l.source,
		File:   file,
		Path:   path,
		Msg:    fmt.Sprintf(format, args...),
	})
}

// addFrom records a fault against a pack other than the list's own source,
// which merging needs when the fault is in the core pack.
func (l *errList) addFrom(src Source, code Code, file, path, format string, args ...any) {
	l.errs = append(l.errs, Error{
		Code:   code,
		Source: src,
		File:   file,
		Path:   path,
		Msg:    fmt.Sprintf(format, args...),
	})
}

// err returns the accumulated faults in a stable order, or nil if there are
// none. Sorting keeps output identical across runs and platforms.
func (l *errList) err() error {
	if len(l.errs) == 0 {
		return nil
	}
	slices.SortStableFunc(l.errs, func(a, b Error) int {
		return cmp.Or(
			cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.Code, b.Code),
			cmp.Compare(a.Msg, b.Msg),
		)
	})
	return l.errs
}
