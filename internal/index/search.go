package index

import "errors"

// Hit is one full-text search result. Kind is the owner kind, entity or
// statement; Snippet is a short excerpt of the body around the match.
type Hit struct {
	ID, Kind, Snippet string
	Rank              float64
}

// Search runs a full-text query over the search table of the index at dbPath,
// best match first.
func Search(dbPath, query string, limit int) ([]Hit, error) {
	return nil, errors.New("index.Search: not implemented")
}
