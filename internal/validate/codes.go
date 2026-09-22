// Package validate checks a loaded world against a merged schema pack.
//
// The split between what blocks a build and what it merely reports is the
// design, not a detail. CI blocks on referential integrity only — dangling
// IDs, unknown relation types, invalid enum values — because those are
// mechanically decidable and always wrong. Everything judgemental is a
// warning: a character at an event outside their lifespan is usually a
// continuity bug and occasionally a flashback, and a gate that is wrong often
// enough trains a team to bypass it, which is worse than no check at all.
//
// Nothing here names a relation. The vocabulary comes from the pack, and the
// one rule that needs to know what an edge means — the lifespan check — binds
// to the participation role, so a world that calls participation something
// else still gets it.
package validate

import "github.com/gmreyer/lore-core/internal/world"

// Blocking errors. These are the spec's list, and each one exits the build
// non-zero.
const (
	// Shape of a document.
	CodeMissingField     world.Code = "missing_field"
	CodeFieldOnWrongType world.Code = "field_on_wrong_type"
	CodeBadEnum          world.Code = "bad_enum"

	// Identity, across the whole world.
	CodeBadID             world.Code = "bad_id"
	CodeDuplicateID       world.Code = "duplicate_id"
	CodeCaseCollision     world.Code = "case_collision"
	CodeFileCaseCollision world.Code = "file_case_collision"

	// The closed vocabulary.
	CodeUnknownEntityType world.Code = "unknown_entity_type"
	CodeUnknownRelation   world.Code = "unknown_relation"
	CodeInverseAuthored   world.Code = "inverse_authored"
	CodeDomainViolation   world.Code = "domain_violation"
	CodeRangeViolation    world.Code = "range_violation"
	CodeUnknownEra        world.Code = "unknown_era"
	CodeUnknownAct        world.Code = "unknown_act"

	// References between documents.
	CodeDangling          world.Code = "dangling_reference"
	CodeWrongKind         world.Code = "wrong_kind"
	CodeStatementEndpoint world.Code = "statement_endpoint"
	CodeUnknownOutcome    world.Code = "unknown_outcome"

	// One fact, authored once.
	CodeDuplicateEdge   world.Code = "duplicate_edge"
	CodeSymmetricMirror world.Code = "symmetric_mirror"
)

// Warnings. Reported, counted, and never fatal.
const (
	CodeLifespanMismatch    world.Code = "lifespan_mismatch"
	CodeOrphan              world.Code = "orphan"
	CodeCanonDependsOnDraft world.Code = "canon_depends_on_draft"
	CodeUnbelievedStatement world.Code = "unbelieved_statement"
	CodeDirectoryMismatch   world.Code = "directory_mismatch"
)
