package resolve

// Source says where a resolved belief came from.
type Source string

const (
	// Explicit is the agent's own belief.
	Explicit Source = "explicit"
	// Inherited is a faction's belief, reached through a membership.
	Inherited Source = "inherited"
	// Common is canon truth, known because the statement is common knowledge.
	Common Source = "common"
	// Canon is canon truth, read omnisciently.
	Canon Source = "canon"
)

// Belief is a statement as one mind holds it, or as canon has it.
type Belief struct {
	Statement string
	Subject   string
	Relation  string
	Object    string
	// Held is true, false, or unresolved (canon only).
	Held   string
	Source Source
	// Via is the faction a belief was inherited from.
	Via          string
	Confidence   string
	AcquiredFrom string
}

// statementsAbout lists the statements whose subject or object is id, as ctx
// sees them. Filled in by the belief chain.
func (r *Resolver) statementsAbout(ctx Context, id string) []Belief {
	return nil
}
