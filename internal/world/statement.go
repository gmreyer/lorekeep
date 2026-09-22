package world

// Statement is a contested fact: a subject–relation–object triple made
// explicit so that it can carry its own canon truth value and so that agents
// can hold positions on it.
//
// Reification is selective and expensive, and the discipline is what keeps it
// affordable: plain typed edges by default, a Statement only when someone
// needs to be wrong. Most of a world — born_in, member_of — is uncontested and
// stays as edges. Promoting one is an explicit authoring act, done when a
// character's misapprehension matters to a scene.
//
// The triple is validated exactly as an authored edge would be: Relation must
// be declared by the merged pack, and the types of Subject and Object must
// satisfy its domain and range. That is the whole point — a Statement is an
// edge someone can be wrong about, so it obeys the same closed vocabulary.
type Statement struct {
	ID   string `yaml:"id"`
	Type string `yaml:"type"` // always StatementType
	// Name is an optional human label. A statement's real identity is its
	// triple; this exists so an editor and a findings list have something
	// short to print.
	Name string `yaml:"name,omitempty"`

	Subject  string `yaml:"subject"`
	Relation string `yaml:"relation"`
	Object   string `yaml:"object"`

	// Truth is the value in canon, which may differ per worldline — so a
	// character can be right in one branch and wrong in another without the
	// character being duplicated.
	Truth      Truth       `yaml:"truth"`
	Status     Status      `yaml:"status"`
	Visibility Visibility  `yaml:"visibility"`
	ValidIn    []Condition `yaml:"valid_in,omitempty"`

	Body   string `yaml:"-"`
	Source Source `yaml:"-"`
}

// HasBody reports whether the statement carries prose explaining the contest.
func (s *Statement) HasBody() bool { return s.Body != "" }
