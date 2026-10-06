package mcp

import "context"

// ProposedFile is one entity or statement file an agent proposes, by its
// path under world/.
type ProposedFile struct {
	Path    string `json:"path" jsonschema:"the file's path under world/, with forward slashes"`
	Content string `json:"content" jsonschema:"the whole file: YAML frontmatter and Markdown body"`
}

// ValidateResult is what validating proposed files found.
type ValidateResult struct {
	Findings []Finding `json:"findings"`
}

// ProposeResult names the proposal that was written.
type ProposeResult struct {
	ID       string    `json:"id"`
	Findings []Finding `json:"findings,omitempty"`
}

// validate checks proposed files against the world without writing anything.
func (s *Server) validate(ctx context.Context, files []ProposedFile) (ValidateResult, error) {
	return ValidateResult{}, ErrNotImplemented
}

// propose writes proposed files under proposals/<id>/ for a human to review.
func (s *Server) propose(ctx context.Context, files []ProposedFile, summary string) (ProposeResult, error) {
	return ProposeResult{}, ErrNotImplemented
}
