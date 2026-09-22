package world

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fixtureWorld is the one fixture world in this repo, shared by this package,
// the validator, the index builder, and the CLI. One copy means one place
// where the format and the vocabulary have to agree.
var fixtureWorld = filepath.Join("..", "..", "testdata", "world-ok", "world")

func TestLoadFixture(t *testing.T) {
	w, fs := Load(fixtureWorld)
	noFindings(t, fs)

	wantEntities := []string{
		"char_kaelen_vor",
		"char_miren",
		"char_orrin",
		"dec_siege_outcome",
		"evt_siege_of_vale",
		"fac_ashen_court",
		"loc_vale_of_orrin",
	}
	var gotEntities []string
	for _, e := range w.Entities {
		gotEntities = append(gotEntities, e.ID)
	}
	if !slices.Equal(gotEntities, wantEntities) {
		t.Errorf("entities = %v, want %v", gotEntities, wantEntities)
	}

	wantStatements := []string{"stmt_orrin_oath"}
	var gotStatements []string
	for _, s := range w.Statements {
		gotStatements = append(gotStatements, s.ID)
	}
	if !slices.Equal(gotStatements, wantStatements) {
		t.Errorf("statements = %v, want %v", gotStatements, wantStatements)
	}
}

// TestLoadOrderIsDeterministic matters more than it looks: the index and the
// snapshot are golden-tested, and a walk that varied with the filesystem would
// make every golden diff unreadable.
func TestLoadOrderIsDeterministic(t *testing.T) {
	first, _ := Load(fixtureWorld)
	for range 3 {
		again, _ := Load(fixtureWorld)
		for i := range first.Entities {
			if first.Entities[i].ID != again.Entities[i].ID {
				t.Fatalf("entity order changed between loads at %d", i)
			}
		}
	}
}

// TestLoadPathsAreSlashed keeps findings and goldens identical on Windows and
// on CI, which are not the same platform here.
func TestLoadPathsAreSlashed(t *testing.T) {
	w, _ := Load(fixtureWorld)
	e, ok := w.Entity("char_kaelen_vor")
	if !ok {
		t.Fatal("char_kaelen_vor not found")
	}
	if got, want := e.Source.File, "characters/kaelen-vor.md"; got != want {
		t.Errorf("Source.File = %q, want %q", got, want)
	}
}

func TestLoadLookups(t *testing.T) {
	w, _ := Load(fixtureWorld)

	if _, ok := w.Entity("char_kaelen_vor"); !ok {
		t.Error("Entity miss on a present id")
	}
	if _, ok := w.Entity("stmt_orrin_oath"); ok {
		t.Error("Entity should not resolve a statement id")
	}
	if _, ok := w.Statement("stmt_orrin_oath"); !ok {
		t.Error("Statement miss on a present id")
	}

	// KindOf is what lets the validator say "that is a statement" rather than
	// "that does not exist", which are very different authoring mistakes.
	if k, ok := w.KindOf("char_kaelen_vor"); !ok || k != KindEntity {
		t.Errorf("KindOf(entity) = %q %v", k, ok)
	}
	if k, ok := w.KindOf("stmt_orrin_oath"); !ok || k != KindStatement {
		t.Errorf("KindOf(statement) = %q %v", k, ok)
	}
	if _, ok := w.KindOf("char_nobody"); ok {
		t.Error("KindOf resolved an id that does not exist")
	}
}

// TestLoadIgnoresNonMarkdown keeps a README or a stray image in a world
// directory from becoming a parse error.
func TestLoadIgnoresNonMarkdown(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "characters")
	write(t, root, "characters/x.md", file("id: char_x\ntype: character\n", ""))
	write(t, root, "characters/notes.txt", []byte("scratch"))
	write(t, root, "README.md.bak", []byte("junk"))

	w, fs := Load(root)
	noFindings(t, fs)
	if len(w.Entities) != 1 {
		t.Errorf("entities = %d, want 1", len(w.Entities))
	}
}

// TestLoadKeepsGoingAfterABadFile: reporting every fault in one pass is the
// point, here as in the schema loader.
func TestLoadKeepsGoingAfterABadFile(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "characters")
	write(t, root, "characters/good.md", file("id: char_good\ntype: character\n", ""))
	write(t, root, "characters/broken.md", []byte("no frontmatter here\n"))
	write(t, root, "characters/alsobad.md", []byte("---\nid: [oops\n---\n"))

	w, fs := Load(root)
	wantCodes(t, fs, CodeNoFrontmatter, CodeParse)
	if len(w.Entities) != 1 || w.Entities[0].ID != "char_good" {
		t.Errorf("the good file should still load: %v", w.Entities)
	}
	for _, f := range fs {
		if f.File == "" || f.Line == 0 {
			t.Errorf("finding is unlocated: %+v", f)
		}
	}
}

// TestLoadDuplicateIDsSurvive: duplicates are a validator error, so the loader
// must not silently drop one or fail. Both documents reach validation.
func TestLoadDuplicateIDsSurvive(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "characters")
	write(t, root, "characters/a.md", file("id: char_x\ntype: character\nname: A\n", ""))
	write(t, root, "characters/b.md", file("id: char_x\ntype: character\nname: B\n", ""))

	w, fs := Load(root)
	noFindings(t, fs)
	if len(w.Entities) != 2 {
		t.Fatalf("entities = %d, want both duplicates kept", len(w.Entities))
	}
	// The lookup resolves to the first in walk order, deterministically.
	e, _ := w.Entity("char_x")
	if e.Name != "A" {
		t.Errorf("lookup resolved to %q, want the first in walk order", e.Name)
	}
}

func TestLoadMissingRoot(t *testing.T) {
	_, fs := Load(filepath.Join("..", "..", "testdata", "no-such-world"))
	wantCodes(t, fs, CodeUnreadable)
}

func mkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
