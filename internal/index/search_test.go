package index

import (
	"fmt"
	"path/filepath"
	"testing"
)

// searchWorld builds a copy of the fixture with extra characters and returns
// the path of its index database. Each extra is {file, id, name, body}.
func searchWorld(t *testing.T, extras ...[4]string) string {
	t.Helper()
	repo := copyTree(t, fixtureRepo)
	for _, x := range extras {
		writeFile(t, repo, "world/characters/"+x[0]+".md",
			"---\nid: "+x[1]+"\ntype: character\nname: "+x[2]+"\nstatus: canon\nvisibility: public\n---\n\n"+x[3]+"\n")
	}
	out := t.TempDir()
	res, err := Build(repo, out)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Findings.HasErrors() {
		t.Fatalf("search world should build clean:\n%v", res.Findings)
	}
	return filepath.Join(out, IndexFile)
}

func hitIDs(hits []Hit) []string {
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	return ids
}

func TestSearchFindsAlias(t *testing.T) {
	db := searchWorld(t)
	hits, err := Search(db, "quiet envoy", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != "char_miren" || hits[0].Kind != "entity" {
		t.Fatalf("hits = %+v, want char_miren first", hits)
	}
}

func TestSearchRanksNameOverBody(t *testing.T) {
	db := searchWorld(t,
		[4]string{"a-body", "char_a_body", "Wanderer", "Once carried a zephyr lantern. The zephyr never went out."},
		[4]string{"b-name", "char_b_name", "Zephyr", "A plain traveller."},
	)
	hits, err := Search(db, "zephyr", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "char_b_name" || hits[1].ID != "char_a_body" {
		t.Fatalf("hits = %v, want the name match first", hitIDs(hits))
	}
	if hits[0].Rank > hits[1].Rank {
		t.Errorf("ranks %v, %v: best match must have the lowest rank", hits[0].Rank, hits[1].Rank)
	}
	if hits[1].Snippet == "" {
		t.Error("a body match should carry a snippet")
	}
}

func TestSearchQuotesSyntax(t *testing.T) {
	db := searchWorld(t)
	for _, q := range []string{`"`, `a OR`, `*`, `NEAR(orrin vale)`, `orrin"`, `-orrin`, "", "   ", "!!!"} {
		hits, err := Search(db, q, 10)
		if err != nil {
			t.Errorf("Search(%q): %v", q, err)
			continue
		}
		switch q {
		case "orrin\"", "-orrin":
			// punctuation is dropped, the word still matches
			if len(hits) == 0 {
				t.Errorf("Search(%q) found nothing", q)
			}
		default:
			if len(hits) != 0 {
				t.Errorf("Search(%q) = %v, want no hits", q, hitIDs(hits))
			}
		}
	}
}

func TestSearchLimit(t *testing.T) {
	var extras [][4]string
	for i := 0; i < 55; i++ {
		extras = append(extras, [4]string{
			fmt.Sprintf("ember-%02d", i), fmt.Sprintf("char_ember_%02d", i), fmt.Sprintf("Keeper %02d", i), "Tends the ember.",
		})
	}
	db := searchWorld(t, extras...)
	for _, c := range []struct{ limit, want int }{{3, 3}, {0, 1}, {-5, 1}, {1000, 50}} {
		hits, err := Search(db, "ember", c.limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != c.want {
			t.Errorf("limit %d: %d hits, want %d", c.limit, len(hits), c.want)
		}
	}
}

func TestSearchMissingDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.db")
	if _, err := Search(missing, "orrin", 5); err == nil {
		t.Fatal("want an error for a missing database")
	}
}
