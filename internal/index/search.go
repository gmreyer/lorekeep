package index

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
)

// Hit is one full-text search result. Kind is the owner kind, entity or
// statement; Snippet is a short excerpt of the body around the match.
//
// Rank is the raw bm25 score: lower is a better match, and hits come back in
// ascending Rank order.
type Hit struct {
	ID, Kind, Snippet string
	Rank              float64
}

// Result limits for Search.
const (
	minSearchLimit = 1
	maxSearchLimit = 50
)

// Search runs a full-text query over the search table of the index at dbPath,
// best match first. limit is clamped to 1-50.
//
// The query is words, not FTS syntax: it is cut into letter and digit runs and
// each run is quoted as its own phrase, so quotes, operators, column filters
// and NEAR are plain text. All words must match. A query with no words returns
// no hits and no error. The database is opened read-only.
func Search(dbPath, query string, limit int) ([]Hit, error) {
	limit = min(max(limit, minSearchLimit), maxSearchLimit)

	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("opening the index: %w", err)
	}
	defer db.Close()

	// bm25 weights follow the table's columns: id, kind (unindexed), name,
	// aliases, body. A name match outranks an alias match, which outranks a
	// match in prose. Ties break on id so the order is deterministic.
	rows, err := db.Query(`
		SELECT id, kind, snippet(search, 4, '', '', '…', 12), bm25(search, 0, 0, 10, 5, 1) AS rank
		FROM search WHERE search MATCH ?
		ORDER BY rank, id LIMIT ?`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("searching the index: %w", err)
	}
	defer rows.Close()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Kind, &h.Snippet, &h.Rank); err != nil {
			return nil, fmt.Errorf("reading a search hit: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searching the index: %w", err)
	}
	return hits, nil
}

// ftsQuery turns user text into an FTS5 MATCH expression of quoted words.
func ftsQuery(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " ")
}

// readOnlyDSN is a SQLite URI that never creates the file. The path is
// escaped so spaces and a Windows drive letter survive.
func readOnlyDSN(path string) string {
	u := url.URL{Path: filepath.ToSlash(path)}
	return "file:" + u.EscapedPath() + "?mode=ro"
}
