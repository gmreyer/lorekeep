// Package world defines the authored file format — entities and statements as
// Markdown with YAML frontmatter — and reads a world directory into memory.
//
// Frontmatter is canonical for the graph; prose is canonical for narrative.
// Relations, IDs, aliases, dates, beliefs, and statuses live in fields, and
// nothing here ever parses anything out of a body. The body is carried through
// for full-text search and for the "entity with no prose" metric, and for
// nothing else.
//
// This package knows the format, not the rules. It reports framing and YAML
// faults; everything that needs the schema pack or a second file — unknown
// relation types, dangling references, domain violations — belongs to
// internal/validate.
package world

// Kind separates the two things a world file can be.
//
// A Statement is not an eighth entity type. It has its own directory, its own
// loader, and its own table in the index, and it may never stand at either end
// of a relation. It shares one thing with entities: the ID namespace, so that
// duplicate and case-collision checks see every identifier in the world at
// once.
type Kind string

const (
	KindEntity    Kind = "entity"
	KindStatement Kind = "statement"
)

// StatementType is the reserved value of an entity file's `type` field that
// routes a document to the statement loader.
//
// Using the same field rather than a second discriminator keeps frontmatter
// canonical with one place to look. It is safe because internal/schema
// reserves the name: no pack may declare an entity type or group called
// "statement", so the name never enters Pack.Types — and since domain and
// range admit only declared types, a statement is structurally incapable of
// being a relation endpoint rather than merely forbidden from being one.
const StatementType = "statement"

// Entity is one node of the graph, as authored.
//
// Type-specific fields are declared here rather than in per-type structs
// because the entity type set is open at the edges — a project pack adds its
// own — so a Go type per entity type would put project vocabulary in core
// code. The validator reports a field that does not belong to the type it was
// authored on.
type Entity struct {
	// ID is stable and opaque: never derived from a name, a title, or a
	// filename, and never renamed. Every edge, belief, and save file points at
	// it, so renaming is free while it is stable and a migration once it is
	// not.
	ID         string      `yaml:"id"`
	Type       string      `yaml:"type"`
	Name       string      `yaml:"name"`
	Aliases    []string    `yaml:"aliases,omitempty"`
	Status     Status      `yaml:"status"`
	Visibility Visibility  `yaml:"visibility"`
	Relations  []Relation  `yaml:"relations,omitempty"`
	Beliefs    []Belief    `yaml:"beliefs,omitempty"`
	Asserts    []Assertion `yaml:"asserts,omitempty"`

	// ValidIn defaults to nil, meaning unconditional — true in every branch.
	ValidIn []Condition `yaml:"valid_in,omitempty"`

	// Lifespan belongs to a character: the interval between birth and death.
	Lifespan *Interval `yaml:"lifespan,omitempty"`
	// Date belongs to an event: when it happened, as an interval.
	Date *Interval `yaml:"date,omitempty"`
	// Outcomes belongs to a decision: the closed set of branches it opens.
	// Every valid_in in the world names one of these.
	Outcomes []string `yaml:"outcomes,omitempty"`

	Body   string `yaml:"-"`
	Source Source `yaml:"-"`
}

// Relation is one authored edge, running from the entity that declares it to
// Target.
//
// Direction is not a field: an edge runs source to target, and its meaning is
// the relation's declared domain-to-range direction. An inverse is never
// authored — the index derives it — so an inverse name appearing here is an
// error rather than a second way to say the same thing.
type Relation struct {
	Type   string `yaml:"type"`
	Target string `yaml:"target"`
	// Priority orders an agent's memberships. Belief inheritance walks
	// factions in this order, which is what makes dual membership with
	// conflicting positions resolve deterministically instead of arbitrarily.
	Priority *int        `yaml:"priority,omitempty"`
	ValidIn  []Condition `yaml:"valid_in,omitempty"`
	Note     string      `yaml:"note,omitempty"`
}

// Belief is an agent holding a position on a Statement.
//
// Value is what the agent takes the statement to be, not what it is. A false
// belief is an agent whose Value disagrees with the statement's canon truth —
// no special machinery, which is what keeps this affordable.
//
// Absence of a belief is ignorance. Ignorance is never modelled explicitly;
// doing so would triple the graph for no gain.
type Belief struct {
	Statement string `yaml:"statement"`
	// Value is required and explicit. Defaulting it would make a diff that
	// flips a belief invisible, and flipping one is the interesting edit.
	Value        *bool       `yaml:"value"`
	Confidence   Confidence  `yaml:"confidence,omitempty"`
	Since        *Interval   `yaml:"since,omitempty"`
	AcquiredFrom string      `yaml:"acquired_from,omitempty"`
	ValidIn      []Condition `yaml:"valid_in,omitempty"`
}

// Assertion is an agent saying a Statement is so.
//
// Asserting one value while believing the other is, structurally, a lie —
// which is why this is a separate list rather than a flag on Belief. Nothing
// special is needed to query for every character currently deceiving another.
type Assertion struct {
	Statement string `yaml:"statement"`
	Value     *bool  `yaml:"value"`
	// To is the audience. Empty means asserted openly.
	To      string      `yaml:"to,omitempty"`
	When    *Interval   `yaml:"when,omitempty"`
	ValidIn []Condition `yaml:"valid_in,omitempty"`
}

// IsAgent is not defined here on purpose: which types hold beliefs is a
// property of the schema pack's "agent" group, not of this package. Hardcoding
// it would put project vocabulary in core code.

// HasBody reports whether the entity carries prose.
func (e *Entity) HasBody() bool { return e.Body != "" }
