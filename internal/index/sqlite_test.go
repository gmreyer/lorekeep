package index

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFTS5IsAvailable(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE search USING fts5(id UNINDEXED, name, body)`); err != nil {
		t.Fatalf("fts5 unavailable: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO search VALUES ('char_x','Kaelen Vor','came to the Vale')`); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM search WHERE search MATCH 'Kaelen'`).Scan(&id); err != nil {
		t.Fatalf("fts5 query: %v", err)
	}
	if id != "char_x" {
		t.Errorf("got %q", id)
	}
}

// TestIndexAnswersQueries checks the index is usable, not merely populated.
// Each query below is one the resolver, the editor, or the wiki will make.
func TestIndexAnswersQueries(t *testing.T) {
	_, out := buildFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(out, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	t.Run("inverse traversal without materialised rows", func(t *testing.T) {
		// "Who is in the Ashen Court" runs member_of backwards. There is no
		// has_member row anywhere; the index on target is what makes this work.
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM edges WHERE target = ? AND relation = ?`,
			"fac_ashen_court", "member_of").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("members of the Ashen Court = %d, want 2", n)
		}
		if err := db.QueryRow(`SELECT count(*) FROM edges WHERE relation = ?`, "has_member").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("found %d has_member rows; inverses are derived, never stored", n)
		}
	})

	t.Run("traversal binds to a role", func(t *testing.T) {
		// Everyone who took part in the siege, through whatever relations this
		// world tags as participation — never through a relation name.
		rows, err := db.Query(`
			SELECT DISTINCT e.source FROM edges e
			JOIN relations r ON r.name = e.relation
			WHERE r.role = ? AND e.target = ?
			ORDER BY e.source`, "participation", "evt_siege_of_vale")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		want := []string{"char_kaelen_vor", "char_miren", "char_orrin", "fac_ashen_court"}
		if !slices.Equal(got, want) {
			t.Errorf("participants = %v, want %v", got, want)
		}
	})

	t.Run("a lie is one join", func(t *testing.T) {
		// An agent asserting one value while believing the other. The spec
		// calls this the feature rather than a side effect, so it should not
		// need machinery.
		var agent, statement string
		err := db.QueryRow(`
			SELECT a.agent, a.statement FROM assertions a
			JOIN beliefs b ON b.agent = a.agent AND b.statement = a.statement
			WHERE a.value != b.value`).Scan(&agent, &statement)
		if err != nil {
			t.Fatalf("no lie found: %v", err)
		}
		if agent != "char_miren" || statement != "stmt_orrin_oath" {
			t.Errorf("got %s lying about %s", agent, statement)
		}
	})

	t.Run("full-text search over names, aliases and prose", func(t *testing.T) {
		for _, tc := range []struct{ query, want string }{
			{"Kaelen", "char_kaelen_vor"},    // name
			{"Knight", "char_kaelen_vor"},    // alias: "The Ashen Knight"
			{"conscript", "char_kaelen_vor"}, // prose
			{"Ashen", "fac_ashen_court"},     // ranked above Kaelen, whose match is an alias
		} {
			var id string
			if err := db.QueryRow(
				`SELECT id FROM search WHERE search MATCH ? ORDER BY rank LIMIT 1`, tc.query).Scan(&id); err != nil {
				t.Errorf("%q: %v", tc.query, err)
				continue
			}
			if id != tc.want {
				t.Errorf("%q matched %q, want %q", tc.query, id, tc.want)
			}
		}
	})

	t.Run("what a decision reaches", func(t *testing.T) {
		// Blast radius: everything conditional on one decision, across every
		// kind of owner, from one table.
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM conditions WHERE decision = ?`,
			"dec_siege_outcome").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("conditions on the siege decision = %d, want 1", n)
		}
	})

	t.Run("a statement is never a legal endpoint", func(t *testing.T) {
		var n int
		if err := db.QueryRow(
			`SELECT count(*) FROM relation_domain_types WHERE type = 'statement'
			 UNION ALL SELECT count(*) FROM relation_range_types WHERE type = 'statement'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("a relation admits a statement at an endpoint")
		}
	})
}
