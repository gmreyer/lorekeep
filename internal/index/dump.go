package index

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// Dump renders an index database as deterministic text.
//
// This exists because a SQLite file is not a reviewable artefact: its bytes
// depend on page allocation and vacuum state, and a golden made of them could
// only ever be accepted, never read. The rule is that golden diffs get read,
// so the golden is text — every table, every row, in a declared order.
//
// A body is rendered as its length rather than its prose. The prose is already
// reviewed in the diff of the file it lives in, and inlining it here would
// bury a structural change under paragraphs. The full text is still checked:
// the snapshot golden carries it verbatim.
func Dump(path string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var b strings.Builder
	for _, t := range dumpTables {
		if err := dumpTable(&b, db, t); err != nil {
			return "", fmt.Errorf("%s: %w", t.name, err)
		}
	}
	return b.String(), nil
}

type tableDump struct {
	name    string
	columns string
	orderBy string
}

// dumpTables lists every table with an explicit column list and an explicit
// order. Both are spelled out rather than discovered, so that adding a column
// shows up in the golden as a deliberate change rather than appearing wherever
// SQLite happens to put it.
var dumpTables = []tableDump{
	{"meta", "key, value", "key"},
	{"entity_types", "name, description", "name"},
	{"groups", "name, type", "name, type"},
	{"roles", "name, description", "name"},
	{"relations", "name, inverse, symmetric, role, description", "name"},
	{"relation_domain", "relation, type", "relation, type"},
	{"relation_range", "relation, type", "relation, type"},
	{"relation_domain_types", "relation, type", "relation, type"},
	{"relation_range_types", "relation, type", "relation, type"},
	{"eras", "ordinal, key, name", "ordinal"},
	{"acts", "ordinal, key, name", "ordinal"},
	{"entities", "id, type, name, status, visibility, visibility_kind, visibility_act, era, earliest, latest, precision, file, length(body)", "id"},
	{"aliases", "entity, ord, alias", "entity, ord"},
	{"edges", "id, source, relation, target, priority, ord, note", "id"},
	{"statements", "id, name, subject, relation, object, truth, status, visibility, visibility_kind, visibility_act, file, length(body)", "id"},
	{"beliefs", "id, agent, statement, value, confidence, since_era, since_earliest, since_latest, acquired_from, ord", "id"},
	{"assertions", "id, agent, statement, value, audience, when_era, when_earliest, when_latest, ord", "id"},
	{"outcomes", "decision, ord, outcome", "decision, ord"},
	{"conditions", "owner_kind, owner, ord, decision, outcome", "owner_kind, owner, ord"},
	{"search", "id, kind, name, aliases, length(body)", "id"},
}

func dumpTable(b *strings.Builder, db *sql.DB, t tableDump) error {
	rows, err := db.Query("SELECT " + t.columns + " FROM " + t.name + " ORDER BY " + t.orderBy)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	fmt.Fprintf(b, "## %s (%s)\n", t.name, strings.Join(cols, ", "))

	values := make([]any, len(cols))
	scan := make([]any, len(cols))
	for i := range values {
		scan[i] = &values[i]
	}

	n := 0
	for rows.Next() {
		if err := rows.Scan(scan...); err != nil {
			return err
		}
		cells := make([]string, len(values))
		for i, v := range values {
			cells[i] = cell(v)
		}
		b.WriteString(strings.Join(cells, "\t"))
		b.WriteString("\n")
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		b.WriteString("(empty)\n")
	}
	b.WriteString("\n")
	return nil
}

// cell renders one value. NULL is spelled out, because the difference between
// an absent era and an empty one is exactly the kind of thing a golden should
// make visible.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return escape(string(x))
	case string:
		return escape(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return escape(fmt.Sprint(x))
	}
}

// escape keeps one row on one line. The dump is tab-separated, so a tab or a
// newline inside a value has to become visible rather than break the shape.
func escape(s string) string {
	if s == "" {
		return `""`
	}
	r := strings.NewReplacer("\\", `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}
