package index

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// update regenerates the goldens.
//
// Regeneration is an explicit flag rather than a keystroke on purpose. A
// golden diff here can look cosmetic and be a visibility field silently
// changing kind, so the workflow has to make accepting one a decision.
var update = flag.Bool("update", false, "regenerate the golden files in testdata/")

var fixtureRepo = filepath.Join("..", "..", "testdata", "world-ok")

// buildFixture runs a full build of the fixture repository into a temp
// directory and returns where the artefacts landed.
func buildFixture(t *testing.T) (*Result, string) {
	t.Helper()
	out := t.TempDir()
	res, err := Build(fixtureRepo, out)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Findings.HasErrors() {
		t.Fatalf("the fixture world should build clean:\n%v", res.Findings)
	}
	if len(res.Written) != 2 {
		t.Fatalf("wrote %v, want an index and a snapshot", res.Written)
	}
	return res, out
}

func TestBuildWritesBothArtefacts(t *testing.T) {
	_, out := buildFixture(t)
	for _, name := range []string{IndexFile, SnapshotFile} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestIndexGolden(t *testing.T) {
	_, out := buildFixture(t)
	got, err := Dump(filepath.Join(out, IndexFile))
	if err != nil {
		t.Fatalf("dumping the index: %v", err)
	}
	compareGolden(t, "index.txt", got)
}

func TestSnapshotGolden(t *testing.T) {
	_, out := buildFixture(t)
	data, err := os.ReadFile(filepath.Join(out, SnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "snapshot.json", string(data))
}

// TestBuildIsReproducible: an unchanged repository must produce identical
// bytes. Nothing records a build time, so a rebuild is verifiable and a golden
// diff means something changed in the world rather than in the clock.
func TestBuildIsReproducible(t *testing.T) {
	_, first := buildFixture(t)
	_, second := buildFixture(t)

	firstDump, err := Dump(filepath.Join(first, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	secondDump, err := Dump(filepath.Join(second, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(firstDump, secondDump); diff != "" {
		t.Errorf("two builds of the same world disagree (-first +second):\n%s", diff)
	}

	firstJSON, err := os.ReadFile(filepath.Join(first, SnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := os.ReadFile(filepath.Join(second, SnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(firstJSON), string(secondJSON)); diff != "" {
		t.Errorf("two snapshots of the same world disagree (-first +second):\n%s", diff)
	}
}

// TestBuildWritesNothingWhenValidationFails: a half-built index of a broken
// world is worse than none, because it would answer questions and the answers
// would be wrong.
func TestBuildWritesNothingWhenValidationFails(t *testing.T) {
	repo := copyTree(t, fixtureRepo)
	broken := "---\nid: char_orrin\ntype: character\nname: Orrin\nstatus: canon\nvisibility: public\n" +
		"relations:\n  - { type: originates_from, target: loc_nowhere }\n---\n"
	writeFile(t, repo, "world/characters/orrin.md", broken)

	out := t.TempDir()
	res, err := Build(repo, out)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !res.Findings.HasErrors() {
		t.Fatal("expected a blocking error")
	}
	if len(res.Written) != 0 {
		t.Errorf("wrote %v, want nothing", res.Written)
	}
	for _, name := range []string{IndexFile, SnapshotFile} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists after a failed build", name)
		}
	}
}

// siblingPair writes two characters to a copy of the fixture, with the given
// relations blocks, and builds it.
func siblingPair(t *testing.T, ilseRelations, tomasRelations string) (*Result, string) {
	t.Helper()
	repo := copyTree(t, fixtureRepo)
	for _, c := range []struct{ file, id, relations string }{
		{"world/characters/ilse-marrow.md", "char_ilse_marrow", ilseRelations},
		{"world/characters/tomas-marrow.md", "char_tomas_marrow", tomasRelations},
	} {
		body := "---\nid: " + c.id + "\ntype: character\nname: " + c.id + "\nstatus: canon\nvisibility: public\n"
		if c.relations != "" {
			body += "relations:\n" + c.relations + "\n"
		}
		writeFile(t, repo, c.file, body+"---\n")
	}
	out := t.TempDir()
	res, err := Build(repo, out)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return res, out
}

// TestSymmetricEdgeIsStoredOnce: a symmetric fact is one edge, as authored.
// Reading it from the other end is the resolver's job, not a second row.
func TestSymmetricEdgeIsStoredOnce(t *testing.T) {
	res, _ := siblingPair(t, "  - { type: sibling_of, target: char_tomas_marrow }", "")
	if res.Findings.HasErrors() {
		t.Fatalf("unexpected errors:\n%v", res.Findings)
	}
	var n int
	for _, e := range res.Index.Entities {
		for _, edge := range e.Edges {
			if edge.Relation == "sibling_of" {
				n++
			}
		}
	}
	if n != 1 {
		t.Errorf("sibling_of stored %d times, want once", n)
	}
}

// TestMirroredSymmetricEdgeWritesNothing: the mirror is a blocking error, so
// the fact can never reach the index twice.
func TestMirroredSymmetricEdgeWritesNothing(t *testing.T) {
	res, out := siblingPair(t,
		"  - { type: sibling_of, target: char_tomas_marrow }",
		"  - { type: sibling_of, target: char_ilse_marrow }")
	if !res.Findings.HasErrors() {
		t.Fatal("expected a blocking error")
	}
	for _, name := range []string{IndexFile, SnapshotFile} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists after a failed build", name)
		}
	}
}

// TestRebuildReplacesTheIndex: the index is derived and disposable, so a
// second build must not append to the first.
func TestRebuildReplacesTheIndex(t *testing.T) {
	out := t.TempDir()
	for range 2 {
		if _, err := Build(fixtureRepo, out); err != nil {
			t.Fatalf("build: %v", err)
		}
	}
	dump, err := Dump(filepath.Join(out, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(dump, "char_kaelen_vor\tcharacter"); n != 1 {
		t.Errorf("char_kaelen_vor appears %d times after a rebuild, want 1", n)
	}
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run: go test ./... -update)", path, err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s is out of date (-want +got):\n%s", name, diff)
	}
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the fixture: %v", err)
	}
	return dst
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
