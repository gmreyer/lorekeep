package world

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var update = flag.Bool("update", false, "regenerate the golden files in testdata/")

// probeKey is the harmless change the stability tests make: a key no type
// declares, set and then deleted.
const probeKey = "x_edit_probe"

// authoredFile is one real world file the round-trip tests run over.
type authoredFile struct {
	name string // slash path, e.g. world-ok/world/... or example/world/...
	src  []byte
}

// authoredFiles returns every world file in the repo's test worlds and in the
// scaffolded example world, so that a save is proven against every shape of
// frontmatter a writer has been shown.
func authoredFiles(t *testing.T) []authoredFile {
	t.Helper()
	repo := filepath.Join("..", "..")
	roots, err := filepath.Glob(filepath.Join(repo, "testdata", "*", "world"))
	if err != nil {
		t.Fatal(err)
	}
	roots = append(roots, filepath.Join(repo, "internal", "scaffold", "files", "example", "world"))

	var files []authoredFile
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(repo, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			rel = strings.TrimPrefix(rel, "testdata/")
			rel = strings.TrimPrefix(rel, "internal/scaffold/files/")
			files = append(files, authoredFile{name: rel, src: src})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 30 {
		t.Fatalf("found only %d world files; the walk is looking in the wrong place", len(files))
	}
	return files
}

func edit(t *testing.T, src []byte, changes ...Change) ([]byte, bool) {
	t.Helper()
	out, changed, err := EditFrontmatter(src, changes)
	if err != nil {
		t.Fatalf("EditFrontmatter: %v\n%s", err, src)
	}
	return out, changed
}

// sameDocument compares two parses, ignoring source positions, which move
// when a save re-flows the block.
func sameDocument(t *testing.T, name string, want, got Document) {
	t.Helper()
	opts := cmp.Options{
		cmpopts.IgnoreFields(Entity{}, "Source"),
		cmpopts.IgnoreFields(Statement{}, "Source"),
		cmpopts.EquateEmpty(),
	}
	if d := cmp.Diff(want, got, opts); d != "" {
		t.Errorf("%s: parse changed (-before +after):\n%s", name, d)
	}
}

func TestEditUnchangedReturnsSource(t *testing.T) {
	for _, f := range authoredFiles(t) {
		out, changed := edit(t, f.src)
		if changed || !bytes.Equal(out, f.src) {
			t.Errorf("%s: a no-op edit changed the file (changed=%v)", f.name, changed)
		}
		// Setting a field to the value it already has is a no-op too.
		doc, _ := Parse(f.name, f.src)
		id := ""
		if doc.Entity != nil {
			id = doc.Entity.ID
		} else if doc.Statement != nil {
			id = doc.Statement.ID
		}
		out, changed = edit(t, f.src, Change{Path: []string{"id"}, Value: id})
		if changed || !bytes.Equal(out, f.src) {
			t.Errorf("%s: re-setting id changed the file", f.name)
		}
	}
}

func TestEditIsStable(t *testing.T) {
	for _, f := range authoredFiles(t) {
		before, fs := Parse(f.name, f.src)
		noFindings(t, fs)

		probed, changed := edit(t, f.src, Change{Path: []string{probeKey}, Value: "probe"})
		if !changed {
			t.Fatalf("%s: setting a new key reported no change", f.name)
		}
		got, fs := Parse(f.name, probed)
		wantCodes(t, fs, CodeUnknownField)
		sameDocument(t, f.name, before, got)

		normal, changed := edit(t, probed, Change{Path: []string{probeKey}, Delete: true})
		if !changed {
			t.Fatalf("%s: deleting the probe reported no change", f.name)
		}
		got, fs = Parse(f.name, normal)
		noFindings(t, fs)
		sameDocument(t, f.name, before, got)

		// A second full round over normalised output must be byte-identical:
		// D10 normalises once, not on every save.
		again, _ := edit(t, normal, Change{Path: []string{probeKey}, Value: "probe"})
		again, _ = edit(t, again, Change{Path: []string{probeKey}, Delete: true})
		if !bytes.Equal(again, normal) {
			t.Errorf("%s: re-encoding is not stable:\n--- first\n%s\n--- second\n%s", f.name, normal, again)
		}

		golden(t, filepath.Join("testdata", "edit", filepath.FromSlash(f.name)), normal)
	}
}

// golden compares got with the file at path, or rewrites it under -update.
// The goldens are the normalised form of every authored file, so a review
// sees exactly what a first save does to hand-written YAML.
func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/world -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden:\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

func TestEditKeepsOrderAndComments(t *testing.T) {
	src := file(`# The heir's file.
id: char_heir # stable, never renamed
type: character
name: The Heir
# Who sees this.
visibility: public
status: canon
aliases: [Heir, The Last]
`, "Body.\n")

	out, changed := edit(t, src,
		Change{Path: []string{"name"}, Value: "The Lost Heir"},
		Change{Path: []string{"aliases"}, Value: []string{"Heir", "The Last", "Lost One"}},
	)
	if !changed {
		t.Fatal("changed = false")
	}
	want := file(`# The heir's file.
id: char_heir # stable, never renamed
type: character
name: The Lost Heir
# Who sees this.
visibility: public
status: canon
aliases: [Heir, The Last, Lost One]
`, "Body.\n")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

func TestEditBodyUntouched(t *testing.T) {
	body := "Prose.\r\n\r\n---\r\n\r\n```yaml\r\n---\r\nid: not_frontmatter\r\n---\r\n```\r\n  trailing spaces   \r\nno final newline"
	src := []byte("\xEF\xBB\xBF\r\n---\r\nid: char_a\r\ntype: character\r\n---  \r\n" + body)

	out, changed := edit(t, src, Change{Path: []string{"name"}, Value: "A"})
	if !changed {
		t.Fatal("changed = false")
	}
	want := "\xEF\xBB\xBF\r\n---\r\nid: char_a\r\ntype: character\r\nname: A\r\n---  \r\n" + body
	if d := cmp.Diff(want, string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

func TestEditCRLF(t *testing.T) {
	src := []byte("---\r\nid: char_a\r\ntype: character\r\nlifespan: { earliest: 1,\r\n  latest: 2 }\r\n---\r\nBody.\r\n")
	out, changed := edit(t, src, Change{Path: []string{"status"}, Value: "canon"})
	if !changed {
		t.Fatal("changed = false")
	}
	want := "---\r\nid: char_a\r\ntype: character\r\nlifespan: {earliest: 1, latest: 2}\r\nstatus: canon\r\n---\r\nBody.\r\n"
	if d := cmp.Diff(want, string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
	if bytes.Contains(bytes.ReplaceAll(out, []byte("\r\n"), nil), []byte("\n")) {
		t.Errorf("bare LF in CRLF output: %q", out)
	}
}

func TestEditAddsKeyAtEnd(t *testing.T) {
	src := file("id: char_a\ntype: character\nlifespan:\n  earliest: 1\n", "")
	out, _ := edit(t, src,
		Change{Path: []string{"status"}, Value: "draft"},
		Change{Path: []string{"lifespan", "latest"}, Value: 9},
	)
	want := file("id: char_a\ntype: character\nlifespan:\n  earliest: 1\n  latest: 9\nstatus: draft\n", "")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

func TestEditDeleteKey(t *testing.T) {
	src := file("id: char_a\ntype: character\nname: A # gone with its key\nstatus: canon\n", "Body.\n")
	out, changed := edit(t, src, Change{Path: []string{"name"}, Delete: true})
	if !changed {
		t.Fatal("changed = false")
	}
	want := file("id: char_a\ntype: character\nstatus: canon\n", "Body.\n")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}

	// Deleting what is not there is no change at all.
	for _, path := range [][]string{{"aliases"}, {"lifespan", "latest"}} {
		out, changed = edit(t, src, Change{Path: path, Delete: true})
		if changed || !bytes.Equal(out, src) {
			t.Errorf("deleting absent %v changed the file", path)
		}
	}
}

func TestEditNestedPath(t *testing.T) {
	src := file("id: evt_a\ntype: event\ndate: { era: third_reign, earliest: 380 }\nvalid_in: null\n", "")
	out, _ := edit(t, src,
		Change{Path: []string{"date", "earliest"}, Value: 381},
		Change{Path: []string{"date", "era"}, Delete: true},
		Change{Path: []string{"lifespan", "earliest"}, Value: 1},
	)
	want := file("id: evt_a\ntype: event\ndate: {earliest: 381}\nvalid_in: null\nlifespan:\n  earliest: 1\n", "")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}

	if _, _, err := EditFrontmatter(src, []Change{{Path: []string{"id", "x"}, Value: 1}}); err == nil {
		t.Error("setting a key under a scalar: err = nil")
	}
	if _, _, err := EditFrontmatter(src, []Change{{Path: nil, Value: 1}}); err == nil {
		t.Error("empty path: err = nil")
	}
}

func TestEditList(t *testing.T) {
	src := file(`id: char_a
type: character
relations:
  - type: member_of
    target: fac_a
    priority: 1
`, "")
	type rel struct {
		Type   string `yaml:"type"`
		Target string `yaml:"target"`
	}
	out, _ := edit(t, src,
		Change{Path: []string{"relations"}, Value: []rel{{"member_of", "fac_a"}, {"originates_from", "loc_b"}}},
		Change{Path: []string{"outcomes"}, Value: []string{"held", "fell"}},
	)
	want := file(`id: char_a
type: character
relations:
  - type: member_of
    target: fac_a
  - type: originates_from
    target: loc_b
outcomes:
  - held
  - fell
`, "")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}

	doc, fs := Parse("a.md", out)
	noFindings(t, fs)
	if got := len(doc.Entity.Relations); got != 2 {
		t.Errorf("relations = %d, want 2", got)
	}
}

func TestEditRejectsUnframedFile(t *testing.T) {
	for name, src := range map[string]string{
		"no fence":     "id: a\n",
		"unterminated": "---\nid: a\n",
		"not a map":    "---\n- a\n---\n",
		"bad yaml":     "---\nid: [a\n---\n",
	} {
		if _, _, err := EditFrontmatter([]byte(src), nil); err == nil {
			t.Errorf("%s: err = nil", name)
		}
	}
}

// inlineRelations is the shape every authored relation list in the repo has:
// a block list of inline items.
var inlineRelations = file(`id: char_a
type: character
relations:
  - { type: member_of, target: fac_a, priority: 1 }
  - { type: originates_from, target: loc_b }
  - { type: participated_in, target: evt_c,
      valid_in: [{ decision: dec_d, outcome: held }] }
`, "Body.\n")

func TestEditListIndex(t *testing.T) {
	out, changed := edit(t, inlineRelations, Change{Path: []string{"relations", "1", "target"}, Value: "loc_z"})
	if !changed {
		t.Fatal("changed = false")
	}
	want := file(`id: char_a
type: character
relations:
  - {type: member_of, target: fac_a, priority: 1}
  - {type: originates_from, target: loc_z}
  - {type: participated_in, target: evt_c, valid_in: [{decision: dec_d, outcome: held}]}
`, "Body.\n")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}

	// Beyond D10 normalisation, exactly one line differs.
	normal, _ := edit(t, inlineRelations, Change{Path: []string{probeKey}, Value: 1})
	normal, _ = edit(t, normal, Change{Path: []string{probeKey}, Delete: true})
	a, b := strings.Split(string(normal), "\n"), strings.Split(string(out), "\n")
	if len(a) != len(b) {
		t.Fatalf("line count %d -> %d", len(a), len(b))
	}
	diff := 0
	for i := range a {
		if a[i] != b[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Errorf("%d lines differ from the normalised source, want 1", diff)
	}

	// Replacing a whole item keeps it inline.
	out, _ = edit(t, inlineRelations, Change{Path: []string{"relations", "0"},
		Value: map[string]any{"type": "member_of", "target": "fac_q"}})
	if !strings.Contains(string(out), "\n  - {target: fac_q, type: member_of}\n") {
		t.Errorf("replaced item not inline:\n%s", out)
	}
}

func TestEditAppendKeepsInlineStyle(t *testing.T) {
	type rel struct {
		Type    string           `yaml:"type"`
		Target  string           `yaml:"target"`
		ValidIn []map[string]any `yaml:"valid_in,omitempty"`
	}
	out, _ := edit(t, inlineRelations, Change{Path: []string{"relations", "-"},
		Value: rel{"sibling_of", "char_b", []map[string]any{{"decision": "dec_d"}}}})
	want := "  - {type: participated_in, target: evt_c, valid_in: [{decision: dec_d, outcome: held}]}\n" +
		"  - {type: sibling_of, target: char_b, valid_in: [{decision: dec_d}]}\n---\n"
	if !strings.Contains(string(out), want) {
		t.Errorf("appended item not inline:\n%s", out)
	}

	// Appending to an absent list creates it.
	src := file("id: char_a\ntype: character\n", "")
	out, _ = edit(t, src, Change{Path: []string{"aliases", "-"}, Value: "A"})
	if want := file("id: char_a\ntype: character\naliases:\n  - A\n", ""); !bytes.Equal(out, want) {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}

	// A block item with an inline nested list keeps the nested list inline.
	block := file(`id: char_a
type: character
relations:
  - type: member_of
    target: fac_veil
    valid_in: [{ decision: dec_heir, outcome: hidden }]
`, "")
	out, _ = edit(t, block, Change{Path: []string{"relations", "-"},
		Value: rel{"member_of", "fac_b", []map[string]any{{"decision": "dec_x", "outcome": "kept"}}}})
	want = `  - type: member_of
    target: fac_b
    valid_in: [{decision: dec_x, outcome: kept}]
`
	if !strings.HasSuffix(string(out), want+"---\n") {
		t.Errorf("appended block item:\n%s", out)
	}
}

func TestEditReplaceListKeepsInlineItems(t *testing.T) {
	type rel struct {
		Type   string `yaml:"type"`
		Target string `yaml:"target"`
	}
	out, _ := edit(t, inlineRelations, Change{Path: []string{"relations"},
		Value: []rel{{"member_of", "fac_a"}, {"sibling_of", "char_b"}}})
	want := file(`id: char_a
type: character
relations:
  - {type: member_of, target: fac_a}
  - {type: sibling_of, target: char_b}
`, "Body.\n")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

func TestEditDeleteListItem(t *testing.T) {
	out, changed := edit(t, inlineRelations, Change{Path: []string{"relations", "0"}, Delete: true})
	if !changed {
		t.Fatal("changed = false")
	}
	want := file(`id: char_a
type: character
relations:
  - {type: originates_from, target: loc_b}
  - {type: participated_in, target: evt_c, valid_in: [{decision: dec_d, outcome: held}]}
`, "Body.\n")
	if d := cmp.Diff(string(want), string(out)); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}

	// A field inside an item.
	out, _ = edit(t, inlineRelations, Change{Path: []string{"relations", "2", "valid_in"}, Delete: true})
	if !strings.Contains(string(out), "\n  - {type: participated_in, target: evt_c}\n") {
		t.Errorf("field not deleted:\n%s", out)
	}
}

func TestEditIndexOutOfRange(t *testing.T) {
	for _, c := range []Change{
		{Path: []string{"relations", "3"}, Value: "x"},
		{Path: []string{"relations", "3", "target"}, Value: "x"},
		{Path: []string{"relations", "3"}, Delete: true},
		{Path: []string{"relations", "-1"}, Value: "x"},
		{Path: []string{"relations", "first"}, Value: "x"},
		{Path: []string{"relations", "-", "target"}, Value: "x"},
		{Path: []string{"relations", "-"}, Delete: true},
	} {
		if _, _, err := EditFrontmatter(inlineRelations, []Change{c}); err == nil {
			t.Errorf("%+v: err = nil", c)
		}
	}
}
