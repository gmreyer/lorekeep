package editor

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/world"
)

// newServerAt starts an editor over a copy of a fixture repo; prepare, when
// set, may change the copy's world files before the first build.
func newServerAt(t *testing.T, fixture string, prepare func(repo string)) (*Server, string) {
	t.Helper()
	repo := t.TempDir()
	for _, sub := range []string{"schema", "world"} {
		if err := os.CopyFS(filepath.Join(repo, sub), os.DirFS(filepath.Join(fixture, sub))); err != nil {
			t.Fatal(err)
		}
	}
	if prepare != nil {
		prepare(repo)
	}
	s, err := New(Config{Repo: repo, Settings: filepath.Join(t.TempDir(), "editor.json")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.host = "127.0.0.1:4321"
	return s, repo
}

// entityFiles lists the world's entity files, world-relative with forward
// slashes, and their entities. Statements are left out: only an entity has
// a page to save from.
func entityFiles(t *testing.T, repo string) map[string]*world.Entity {
	t.Helper()
	out := map[string]*world.Entity{}
	root := filepath.Join(repo, index.WorldDir)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if doc, _ := world.Parse(rel, data); doc.Entity != nil {
			out[rel] = doc.Entity
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// splitFile cuts a world file at the end of its closing fence line.
func splitFile(t *testing.T, data []byte) (front, body []byte) {
	t.Helper()
	first := bytes.Index(data, []byte("---"))
	nl := bytes.IndexByte(data[first:], '\n')
	rest := data[first+nl+1:]
	for off := 0; off < len(rest); {
		end := bytes.IndexByte(rest[off:], '\n')
		line := rest[off:]
		if end >= 0 {
			line = rest[off : off+end+1]
		}
		if strings.TrimRight(string(line), "\r\n") == "---" {
			cut := first + nl + 1 + off + len(line)
			return data[:cut], data[cut:]
		}
		if end < 0 {
			break
		}
		off += end + 1
	}
	t.Fatalf("no closing fence in\n%s", data)
	return nil, nil
}

// frontmatter decodes a file's frontmatter block, fences stripped: its value
// and its top-level keys in file order.
func frontmatter(t *testing.T, front []byte) (value map[string]any, keys []string) {
	t.Helper()
	s := strings.ReplaceAll(string(front), "\r\n", "\n")
	s = strings.TrimPrefix(strings.TrimSpace(s), "---")
	s = strings.TrimSuffix(s, "---")
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	m := n.Content[0]
	for i := 0; i < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	if err := n.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value, keys
}

// without removes the edited path from a decoded frontmatter value, so the
// rest can be compared. A last segment "-" drops the whole list.
func without(v any, p []string) any {
	if len(p) == 0 {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		c := maps.Clone(x)
		if len(p) == 1 || p[1] == "-" {
			delete(c, p[0])
		} else if sub, ok := c[p[0]]; ok {
			c[p[0]] = without(sub, p[1:])
		}
		return c
	case []any:
		i, err := strconv.Atoi(p[0])
		if err != nil || i >= len(x) {
			return x
		}
		c := slices.Clone(x)
		if len(p) == 1 {
			c[i] = nil
		} else {
			c[i] = without(c[i], p[1:])
		}
		return c
	}
	return v
}

// worldFiles reads every file under world/, by world-relative path.
func worldFiles(t *testing.T, repo string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	root := filepath.Join(repo, index.WorldDir)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// openDraft opens a draft through the editor and applies fn to it. An entity
// absent from the settings' worldline cannot be opened by the editor (the
// read context hides it); its draft is started from disk the way startDraft
// does, so a save is still shown to keep its file intact.
func openDraft(t *testing.T, s *Server, id, rel string, fn func(d *Draft)) {
	t.Helper()
	err := s.editDraft(id, func(d *Draft) error { fn(d); return nil })
	if !errors.Is(err, resolve.ErrUnknownEntity) {
		if err != nil {
			t.Fatalf("%s: draft: %v", rel, err)
		}
		return
	}
	data, stamp, err := s.readWorldFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	d := &Draft{ID: id, File: rel, Base: data, Stamp: stamp}
	fn(d)
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	s.drafts.m[id] = d
}

// savedClean is the whole notice of a save after which the world builds.
const savedClean = `<span class="lk-save-notice lk-pass">Saved</span>`

const keptComment = "# kept by every save"

func saveURL(id string) string { return "/entity/" + id + "/save" }

// fieldEdit is one kind of edit a writer makes, as the forms put it in a
// draft.
type fieldEdit struct {
	kind string
	// path is the changed frontmatter path; nil for a body edit.
	path    []string
	value   any
	body    string
	applies func(e *world.Entity) bool
	// want is the entity as the edit should leave it.
	want func(e world.Entity) world.Entity
}

func fieldEdits(rel string) []fieldEdit {
	alias := "Saved Alias of " + path.Base(rel)
	const note = "noted by a save"
	body := "Prose rewritten by a save of " + rel + "."
	always := func(*world.Entity) bool { return true }
	return []fieldEdit{
		{kind: "alias", path: []string{"aliases", "-"}, value: alias, applies: always,
			want: func(e world.Entity) world.Entity { e.Aliases = append(slices.Clone(e.Aliases), alias); return e }},
		{kind: "name", path: []string{"name"}, value: "Renamed " + path.Base(rel), applies: always,
			want: func(e world.Entity) world.Entity { e.Name = "Renamed " + path.Base(rel); return e }},
		{kind: "relation note", path: []string{"relations", "0", "note"}, value: note,
			applies: func(e *world.Entity) bool { return len(e.Relations) > 0 },
			want: func(e world.Entity) world.Entity {
				e.Relations = slices.Clone(e.Relations)
				e.Relations[0].Note = note
				return e
			}},
		{kind: "body", body: body, applies: always,
			want: func(e world.Entity) world.Entity { e.Body = body; return e }},
	}
}

// TestSaveOnlyEditedFieldChanges saves each kind of edit in every entity file
// of both fixtures, and of a CRLF copy, and checks that nothing else moved:
// not another file, not the body, not a comment, not a key, not a value
// (Step 6 review focus 1, D10).
func TestSaveOnlyEditedFieldChanges(t *testing.T) {
	resolveRepo := filepath.Join("..", "..", "testdata", "world-resolve")
	for _, v := range []struct {
		name    string
		fixture string
		crlf    bool
	}{
		{"world-ok", fixtureRepo, false},
		{"world-resolve", resolveRepo, false},
		{"world-ok-crlf", fixtureRepo, true},
		{"world-resolve-crlf", resolveRepo, true},
	} {
		t.Run(v.name, func(t *testing.T) {
			eol := "\n"
			if v.crlf {
				eol = "\r\n"
			}
			// A comment above the name of every entity, so a save that drops
			// or moves comments shows.
			s, repo := newServerAt(t, v.fixture, func(repo string) {
				for rel := range entityFiles(t, repo) {
					p := filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))
					data, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
					data = bytes.Replace(data, []byte("\nname:"), []byte("\n"+keptComment+"\nname:"), 1)
					data = bytes.ReplaceAll(data, []byte("\n"), []byte(eol))
					if err := os.WriteFile(p, data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
			files := entityFiles(t, repo)
			if len(files) == 0 {
				t.Fatal("no entity files")
			}
			saves := 0
			for rel := range files {
				for _, ed := range fieldEdits(rel) {
					cur := entityFiles(t, repo)[rel]
					if !ed.applies(cur) {
						continue
					}
					checkEdit(t, s, repo, rel, cur, ed, eol)
					saves++
				}
			}
			t.Logf("%d saves over %d files", saves, len(files))
		})
	}
}

// checkEdit makes one edit to the entity in rel, saves it through the route,
// and checks that it changed exactly what it should.
func checkEdit(t *testing.T, s *Server, repo, rel string, old *world.Entity, ed fieldEdit, eol string) {
	t.Helper()
	what := rel + " (" + ed.kind + ")"
	before := worldFiles(t, repo)
	openDraft(t, s, old.ID, rel, func(d *Draft) {
		if ed.path != nil {
			d.Changes = append(d.Changes, world.Change{Path: ed.path, Value: ed.value})
		} else {
			d.Body = &ed.body
		}
	})
	w := do(t, s, "POST", saveURL(old.ID), nil)
	if got := strings.TrimSpace(w.Body.String()); w.Code != http.StatusOK || got != savedClean {
		t.Fatalf("%s: save: %d %s", what, w.Code, got)
	}
	if s.dirty(old.ID) {
		t.Errorf("%s: still dirty after the save", what)
	}
	after := worldFiles(t, repo)

	// Every other file is untouched, and no file appeared or vanished.
	if len(after) != len(before) {
		t.Errorf("%s: %d files under world/, want %d", what, len(after), len(before))
	}
	for f, data := range before {
		if f != rel && !bytes.Equal(after[f], data) {
			t.Errorf("%s: %s changed too", what, f)
		}
	}

	fb, bb := splitFile(t, before[rel])
	fa, ba := splitFile(t, after[rel])
	if ed.path == nil {
		if !bytes.Equal(fb, fa) {
			t.Errorf("%s: frontmatter changed:\n%s\n→\n%s", what, fb, fa)
		}
	} else if !bytes.Equal(bb, ba) {
		t.Errorf("%s: body changed:\n%q\n→\n%q", what, bb, ba)
	}
	if eol == "\r\n" {
		if n := bytes.Count(after[rel], []byte("\n")); n != bytes.Count(after[rel], []byte("\r\n")) {
			t.Errorf("%s: a CRLF file has bare LF line ends:\n%q", what, after[rel])
		}
	}
	if !bytes.Contains(fa, []byte(keptComment+eol+"name:")) {
		t.Errorf("%s: the comment above name: was dropped or moved:\n%s", what, fa)
	}

	vb, kb := frontmatter(t, fb)
	va, ka := frontmatter(t, fa)
	wantKeys := kb
	if ed.path != nil && !slices.Contains(kb, ed.path[0]) {
		wantKeys = append(slices.Clone(kb), ed.path[0])
	}
	if !slices.Equal(ka, wantKeys) {
		t.Errorf("%s: keys %v → %v, want %v", what, kb, ka, wantKeys)
	}
	if ed.path != nil {
		if diff := cmp.Diff(without(vb, ed.path), without(va, ed.path)); diff != "" {
			t.Errorf("%s: fields other than %v changed (-before +after):\n%s", what, ed.path, diff)
		}
	}

	doc, parsed := world.Parse(rel, after[rel])
	if doc.Entity == nil {
		t.Fatalf("%s: does not parse after the save: %v", what, parsed)
	}
	want := ed.want(*old)
	opts := []cmp.Option{cmpopts.IgnoreFields(world.Entity{}, "Source")}
	if ed.path == nil {
		opts = append(opts, cmpopts.IgnoreFields(world.Entity{}, "Body"))
		if got := strings.TrimSpace(strings.ReplaceAll(doc.Entity.Body, "\r\n", "\n")); got != ed.body {
			t.Errorf("%s: body %q, want %q", what, got, ed.body)
		}
	}
	if diff := cmp.Diff(&want, doc.Entity, opts...); diff != "" {
		t.Errorf("%s: entity after the save (-want +got):\n%s", what, diff)
	}
}

// TestSaveKeepsCommentsInPlace saves a file with a top-level comment, an
// inline comment and a comment inside a list, and finds each where it was.
func TestSaveKeepsCommentsInPlace(t *testing.T) {
	const rel = "characters/kaelen-vor.md"
	s, repo := newServerAt(t, fixtureRepo, func(repo string) {
		p := filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := world.Parse(rel, data)
		if doc.Entity == nil || len(doc.Entity.Aliases) == 0 {
			t.Fatalf("%s: want an entity with aliases", rel)
		}
		var out []string
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "id:"):
				out = append(out, "# top-level comment", line)
			case strings.HasPrefix(line, "name:"):
				out = append(out, line+" # inline comment")
			case strings.HasPrefix(line, "aliases:"):
				out = append(out, "aliases:", "  # list comment")
				for _, a := range doc.Entity.Aliases {
					out = append(out, "  - "+a)
				}
			default:
				out = append(out, line)
			}
		}
		if err := os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e := entityFiles(t, repo)[rel]
	firstAlias := e.Aliases[0]

	check := func(step string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		front, _ := splitFile(t, data)
		lines := strings.Split(string(front), "\n")
		at := func(prefix string) int {
			for i, l := range lines {
				if strings.HasPrefix(l, prefix) {
					return i
				}
			}
			t.Fatalf("%s: no line %q in\n%s", step, prefix, front)
			return -1
		}
		if i := at("# top-level comment"); !strings.HasPrefix(lines[i+1], "id:") {
			t.Errorf("%s: the top-level comment moved:\n%s", step, front)
		}
		if !strings.HasSuffix(lines[at("name:")], "# inline comment") {
			t.Errorf("%s: the inline comment moved:\n%s", step, front)
		}
		i := at("  # list comment")
		if !strings.HasPrefix(lines[i-1], "aliases:") || !strings.HasPrefix(lines[i+1], "  - ") ||
			!strings.Contains(lines[i+1], firstAlias) {
			t.Errorf("%s: the list comment moved:\n%s", step, front)
		}
	}
	check("before")
	for _, c := range []world.Change{
		{Path: []string{"aliases", "-"}, Value: "Another"},
		{Path: []string{"name"}, Value: "Kaelen the Saved"},
	} {
		openDraft(t, s, e.ID, rel, func(d *Draft) { d.Changes = append(d.Changes, c) })
		if w := do(t, s, "POST", saveURL(e.ID), nil); strings.TrimSpace(w.Body.String()) != savedClean {
			t.Fatalf("save %v: %s", c.Path, w.Body)
		}
		check("after " + strings.Join(c.Path, "."))
	}
}

// TestSaveRefusesExternallyChangedFile pins Q5: a file written behind the
// editor's back is never overwritten, and nothing is merged.
func TestSaveRefusesExternallyChangedFile(t *testing.T) {
	s, repo := newTestServer(t)
	const id = "char_kaelen_vor"
	p := filepath.Join(repo, index.WorldDir, "characters", "kaelen-vor.md")
	err := s.editDraft(id, func(d *Draft) error {
		d.Changes = append(d.Changes, world.Change{Path: []string{"aliases", "-"}, Value: "Mine"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	outside, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	outside = append(outside, []byte("\nWritten in another program.\n")...)
	if err := os.WriteFile(p, outside, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, "POST", saveURL(id), nil)
	body := w.Body.String()
	if !strings.Contains(body, "changed outside the editor") || !strings.Contains(body, "lk-error") {
		t.Errorf("notice = %s, want the changed-outside error", body)
	}
	if !strings.Contains(body, "/entity/"+id+"/discard") {
		t.Errorf("notice offers no Reload: %s", body)
	}
	if w.Header().Get("HX-Trigger") != "" {
		t.Errorf("a refused save triggers %q", w.Header().Get("HX-Trigger"))
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, outside) {
		t.Errorf("the outside change was overwritten:\n%s", got)
	}
	if !s.dirty(id) {
		t.Error("the draft was dropped")
	}

	// Reload drops the draft and reloads the page from disk.
	w = do(t, s, "POST", "/entity/"+id+"/discard", nil)
	if w.Header().Get("HX-Refresh") != "true" {
		t.Errorf("discard: HX-Refresh %q", w.Header().Get("HX-Refresh"))
	}
	if s.dirty(id) {
		t.Error("discard kept the draft")
	}
}

// TestSaveWritesNothingWhenUnchanged pins D10: a draft whose changes net to
// nothing leaves the file alone, mtime included.
func TestSaveWritesNothingWhenUnchanged(t *testing.T) {
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	const rel = "characters/kaelen-vor.md"
	s, repo := newServerAt(t, fixtureRepo, func(repo string) {
		if err := os.Chtimes(filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel)), old, old); err != nil {
			t.Fatal(err)
		}
	})
	p := filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))
	before, _ := os.ReadFile(p)
	e := entityFiles(t, repo)[rel]
	err := s.editDraft(e.ID, func(d *Draft) error {
		d.Changes = append(d.Changes, world.Change{Path: []string{"name"}, Value: e.Name})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", saveURL(e.ID), nil)
	if got := strings.TrimSpace(w.Body.String()); got != savedClean {
		t.Errorf("notice = %s", got)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("mtime %v, want %v: the file was written", info.ModTime(), old)
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(after, before) {
		t.Errorf("bytes changed:\n%s", after)
	}
	if s.dirty(e.ID) {
		t.Error("the draft survived the save")
	}

	// No draft at all: a quiet notice, nothing written.
	w = do(t, s, "POST", saveURL(e.ID), nil)
	if !strings.Contains(w.Body.String(), "Nothing to save") || !strings.Contains(w.Body.String(), "lk-quiet") {
		t.Errorf("notice = %s, want Nothing to save", w.Body)
	}
}

// TestSaveRebuildsAndReportsFindings saves a broken link: the file is
// written, the world rebuilt, and the errors reported in the notice and the
// status chips.
func TestSaveRebuildsAndReportsFindings(t *testing.T) {
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	e := entityFiles(t, repo)[rel]
	broken := e.Relations[0]
	broken.Target = "fac_nowhere"
	broken.Priority = nil
	err := s.editDraft(e.ID, func(d *Draft) error {
		d.Changes = append(d.Changes, world.Change{Path: []string{"relations", "-"}, Value: broken})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", saveURL(e.ID), nil)
	body := w.Body.String()
	if !strings.Contains(body, "Saved · the world has 1 error") || !strings.Contains(body, "lk-error") {
		t.Errorf("notice = %s, want the error count", body)
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), eventSaved) {
		t.Errorf("HX-Trigger %q, want %s", w.Header().Get("HX-Trigger"), eventSaved)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))); !bytes.Contains(data, []byte("fac_nowhere")) {
		t.Errorf("the file was not saved:\n%s", data)
	}
	st := do(t, s, "GET", "/status?"+tabsQuery(Tabs{IDs: []string{e.ID}, Active: e.ID}), nil)
	if !strings.Contains(st.Body.String(), "chip error") {
		t.Errorf("status = %s, want the error chip", st.Body)
	}
	if _, err := os.Stat(filepath.Join(repo, index.BuildDir, buildLockFile)); err == nil {
		t.Error("the build lock was left behind")
	}
}

// TestSaveWritesCreatedEntities saves an entity with a link to one made in
// its relation picker: both files land in one save, and the world builds.
func TestSaveWritesCreatedEntities(t *testing.T) {
	s, repo := newTestServer(t)
	files := entityFiles(t, repo)
	const rel = "characters/kaelen-vor.md"
	e := files[rel]
	// The new entity is a copy of an existing target, with its own ID.
	target := e.Relations[0].Target
	var tmplRel string
	for r, x := range files {
		if x.ID == target {
			tmplRel = r
		}
	}
	tmpl, err := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(tmplRel)))
	if err != nil {
		t.Fatal(err)
	}
	newID := target + "_k3x9qz"
	content, _, err := world.EditFrontmatter(tmpl, []world.Change{
		{Path: []string{"id"}, Value: newID},
		{Path: []string{"name"}, Value: "The New One"},
		{Path: []string{"aliases"}, Delete: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	newRel := path.Join(path.Dir(tmplRel), "fresh", "the-new-one.md")
	link := e.Relations[0]
	link.Target = newID
	link.Priority = nil
	err = s.editDraft(e.ID, func(d *Draft) error {
		d.Created = append(d.Created, Created{ID: newID, Type: files[tmplRel].Type, Name: "The New One", File: newRel, Content: content})
		d.Changes = append(d.Changes, world.Change{Path: []string{"relations", "-"}, Value: link})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", saveURL(e.ID), nil)
	if !strings.Contains(w.Body.String(), "Saved") || !strings.Contains(w.Body.String(), "lk-pass") {
		t.Fatalf("notice = %s, want a clean save", w.Body)
	}
	got, err := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(newRel)))
	if err != nil || !bytes.Equal(got, content) {
		t.Errorf("created file: %v\n%s", err, got)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))); !bytes.Contains(data, []byte(newID)) {
		t.Errorf("the link was not saved:\n%s", data)
	}
	if _, _, ok := s.created(newID); ok {
		t.Error("the created entity is still pending")
	}
	// No temporary file is left in either folder.
	for _, dir := range []string{path.Dir(newRel), path.Dir(rel)} {
		ents, _ := os.ReadDir(filepath.Join(repo, index.WorldDir, filepath.FromSlash(dir)))
		for _, d := range ents {
			if strings.HasSuffix(d.Name(), ".tmp") {
				t.Errorf("temporary file %s left in %s", d.Name(), dir)
			}
		}
	}
}

// TestSaveRefusesExistingCreatedFile: a created entity whose file appeared
// on disk is refused like a file changed outside, and nothing is written.
func TestSaveRefusesExistingCreatedFile(t *testing.T) {
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	p := filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))
	before, _ := os.ReadFile(p)
	e := entityFiles(t, repo)[rel]
	err := s.editDraft(e.ID, func(d *Draft) error {
		d.Created = append(d.Created, Created{ID: "x_abcdef", Name: "Taken", File: "characters/miren.md", Content: []byte("x")})
		d.Changes = append(d.Changes, world.Change{Path: []string{"aliases", "-"}, Value: "More"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", saveURL(e.ID), nil)
	if !strings.Contains(w.Body.String(), "lk-error") || !strings.Contains(w.Body.String(), "/discard") {
		t.Errorf("notice = %s, want a refusal with Reload", w.Body)
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(after, before) {
		t.Error("the entity was written")
	}
	if !s.dirty(e.ID) {
		t.Error("the draft was dropped")
	}
}

// TestBuildLockRemovedAfterFailedRebuild breaks the schema pack behind a
// save: the file is saved, the notice says the world did not build, and the
// lock is gone.
func TestBuildLockRemovedAfterFailedRebuild(t *testing.T) {
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	e := entityFiles(t, repo)[rel]
	err := s.editDraft(e.ID, func(d *Draft) error {
		d.Changes = append(d.Changes, world.Change{Path: []string{"aliases", "-"}, Value: "Later"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, index.SchemaDir, "pack.yaml"), []byte(": not yaml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", saveURL(e.ID), nil)
	if !strings.Contains(w.Body.String(), "did not build") || !strings.Contains(w.Body.String(), "lk-error") {
		t.Errorf("notice = %s, want the failed build", w.Body)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))); !bytes.Contains(data, []byte("Later")) {
		t.Error("the file was not saved")
	}
	if _, err := os.Stat(filepath.Join(repo, index.BuildDir, buildLockFile)); err == nil {
		t.Error("the build lock was left behind")
	}
}

// TestBuildLock pins the lock's rules: a stale lock is taken over, a live
// one is waited for and then refused.
func TestBuildLock(t *testing.T) {
	s, repo := newTestServer(t)
	lock := filepath.Join(repo, index.BuildDir, buildLockFile)

	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-buildLockStale - time.Minute)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := s.withBuildLock(func() error { ran = true; return nil }); err != nil || !ran {
		t.Errorf("stale lock: ran %v, err %v", ran, err)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Error("the lock was left behind")
	}

	// A lock taken over by another writer while fn ran is theirs: the
	// holder does not remove it.
	other := []byte("another writer's token\n")
	err := s.withBuildLock(func() error { return os.WriteFile(lock, other, 0o644) })
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(lock); err != nil || !bytes.Equal(got, other) {
		t.Errorf("another writer's lock was removed: %q %v", got, err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}

	if testing.Short() {
		return
	}
	// A stale lock that cannot be removed (here a directory) is waited for
	// until the deadline, then refused: never spun on.
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.withBuildLock(func() error { return errors.New("ran under a held lock") }) }()
	select {
	case err := <-done:
		t.Logf("undeletable stale lock: %v", err)
		if err == nil || err.Error() == "ran under a held lock" {
			t.Errorf("undeletable stale lock: err %v", err)
		}
	case <-time.After(3 * buildLockWait):
		t.Fatal("withBuildLock spins on an undeletable stale lock")
	}
	if info, err := os.Stat(lock); err != nil || !info.IsDir() {
		t.Errorf("the undeletable lock was removed: %v", err)
	}
}

// TestSaveUnderLiveLock saves while another writer holds the build lock:
// the file is saved, the draft dropped, the notice says the world was not
// checked, and the other writer's lock is left alone.
func TestSaveUnderLiveLock(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the build lock")
	}
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	e := entityFiles(t, repo)[rel]
	lock := filepath.Join(repo, index.BuildDir, buildLockFile)
	if err := os.WriteFile(lock, []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openDraft(t, s, e.ID, rel, func(d *Draft) {
		d.Changes = append(d.Changes, world.Change{Path: []string{"aliases", "-"}, Value: "Locked Out"})
	})
	w := do(t, s, "POST", saveURL(e.ID), nil)
	body := w.Body.String()
	if !strings.Contains(body, "Saved · not checked yet") || !strings.Contains(body, "lk-error") {
		t.Errorf("notice = %s", body)
	}
	if s.dirty(e.ID) {
		t.Error("the saved draft was kept")
	}
	if data, _ := os.ReadFile(filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))); !bytes.Contains(data, []byte("Locked Out")) {
		t.Error("the file was not saved")
	}
	if got, _ := os.ReadFile(lock); string(got) != "held\n" {
		t.Errorf("the other writer's lock is %q", got)
	}
}

// TestSaveReportsPartialWrite fails the entity's write after its created
// file is on disk: the notice names the file already written, the draft
// keeps the edit, and the created entry leaves it.
func TestSaveReportsPartialWrite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a read-only file refuses a rename over it on Windows only")
	}
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	e := entityFiles(t, repo)[rel]
	p := filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p, 0o644)
	const newRel = "characters/fresh-one.md"
	openDraft(t, s, e.ID, rel, func(d *Draft) {
		d.Created = append(d.Created, Created{ID: e.ID + "_q1w2e3", Name: "Fresh One", File: newRel, Content: []byte("---\n---\n")})
		d.Changes = append(d.Changes, world.Change{Path: []string{"aliases", "-"}, Value: "Stuck"})
	})
	w := do(t, s, "POST", saveURL(e.ID), nil)
	body := w.Body.String()
	if !strings.Contains(body, "Not saved") || !strings.Contains(body, "already written: world/"+newRel) {
		t.Errorf("notice = %s, want the created file named", body)
	}
	if _, err := os.Stat(filepath.Join(repo, index.WorldDir, filepath.FromSlash(newRel))); err != nil {
		t.Errorf("the created file: %v", err)
	}
	d, _ := s.draft(e.ID)
	if !d.Dirty() || len(d.Created) != 0 || len(d.Changes) != 1 {
		t.Errorf("draft after the failure: %d created, %d changes", len(d.Created), len(d.Changes))
	}
}

// TestSaveRefusesCreatedPathOutsideWorld writes nothing for a created file
// that is not a Markdown file inside world/.
func TestSaveRefusesCreatedPathOutsideWorld(t *testing.T) {
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	e := entityFiles(t, repo)[rel]
	before := worldFiles(t, repo)
	for _, bad := range []string{"../escape.md", "characters/notes.txt", "/abs.md", "characters/../../x.md"} {
		openDraft(t, s, e.ID, rel, func(d *Draft) {
			d.Created = []Created{{ID: e.ID + "_a1b2c3", Name: "Bad", File: bad, Content: []byte("x")}}
			d.Changes = []world.Change{{Path: []string{"aliases", "-"}, Value: "Nope"}}
		})
		w := do(t, s, "POST", saveURL(e.ID), nil)
		if !strings.Contains(w.Body.String(), "Not saved") {
			t.Errorf("%s: notice = %s", bad, w.Body)
		}
	}
	if after := worldFiles(t, repo); !maps.EqualFunc(before, after, bytes.Equal) {
		t.Error("world/ changed")
	}
	if _, err := os.Stat(filepath.Join(repo, "escape.md")); err == nil {
		t.Error("a file was written outside world/")
	}
}

// TestSaveReportsUnreadableFile reports a file that cannot be read as that,
// not as a change made outside the editor.
func TestSaveReportsUnreadableFile(t *testing.T) {
	s, repo := newTestServer(t)
	const rel = "characters/kaelen-vor.md"
	e := entityFiles(t, repo)[rel]
	openDraft(t, s, e.ID, rel, func(d *Draft) {
		d.Changes = append(d.Changes, world.Change{Path: []string{"aliases", "-"}, Value: "Unread"})
	})
	p := filepath.Join(repo, index.WorldDir, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil { // reads fail, and it exists
		t.Fatal(err)
	}
	w := do(t, s, "POST", saveURL(e.ID), nil)
	body := w.Body.String()
	if !strings.Contains(body, "Not saved") || strings.Contains(body, "changed outside") {
		t.Errorf("notice = %s, want the read error", body)
	}
	if !s.dirty(e.ID) {
		t.Error("the draft was dropped")
	}
}
