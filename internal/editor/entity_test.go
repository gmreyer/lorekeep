package editor

import (
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

var (
	structureAside = regexp.MustCompile(`(?s)<aside class="lk-structure".*?</aside>`)
	tagRe          = regexp.MustCompile(`(?s)<[^>]*>`)
	idLine         = regexp.MustCompile(`(?m)^id:\s*(\S+)\s*$`)
)

// visibleText is a page's text as a writer reads it: the structure panel
// (the one place IDs are shown) removed, then the tags.
func visibleText(page string) string {
	page = structureAside.ReplaceAllString(page, "")
	return html.UnescapeString(tagRe.ReplaceAllString(page, " "))
}

// worldIDs are every entity and statement ID in the repo's world.
func worldIDs(t *testing.T, repo string) []string {
	t.Helper()
	var ids []string
	err := filepath.WalkDir(filepath.Join(repo, "world"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := idLine.FindSubmatch(data); m != nil {
			ids = append(ids, string(m[1]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("no IDs in the fixture")
	}
	return ids
}

// entitiesOf lists the summaries of every entity under the editor's context.
func entitiesOf(t *testing.T, s *Server) []resolve.Summary {
	t.Helper()
	var out []resolve.Summary
	err := s.read(func(cur *loaded) error {
		var err error
		out, err = cur.res.Entities(s.readContext())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEntityPageHidesID: no page of an entity, and not its fields form, shows
// an entity or statement ID; only the structure panel does.
func TestEntityPageHidesID(t *testing.T) {
	s, repo := newTestServer(t)
	ids := worldIDs(t, repo)
	for _, e := range entitiesOf(t, s) {
		for _, target := range []string{
			"/entity/" + e.ID, "/entity/" + e.ID + "/fields", "/entity/" + e.ID + "/fields/line",
		} {
			w := do(t, s, "GET", target, nil)
			if w.Code != 200 {
				t.Fatalf("GET %s: %d\n%s", target, w.Code, w.Body)
			}
			text := visibleText(w.Body.String())
			for _, id := range ids {
				if strings.Contains(text, id) {
					t.Errorf("GET %s shows the ID %s", target, id)
				}
			}
		}
	}
}

func TestEntityPageShape(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(t, s, "GET", "/entity/char_kaelen_vor", nil)
	body := w.Body.String()
	for _, want := range []string{
		`<h1 class="lk-title" id="lk-title">Kaelen Vor</h1>`,
		`<p class="lk-type">character</p>`,
		`id="lk-fields-toggle"`,
		`hx-post="/entity/char_kaelen_vor/body"`,
		`hx-trigger="input changed delay:500ms"`,
		"Kaelen Vor came to the Vale as a conscript",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	text := strings.Join(strings.Fields(visibleText(body)), " ")
	for _, want := range []string{"aliases The Ashen Knight, Vor", "lifespan third_reign 380–419"} {
		if !strings.Contains(text, want) {
			t.Errorf("fields line lacks %q in %q", want, text)
		}
	}
	if w := do(t, s, "GET", "/entity/char_nobody", nil); w.Code != 404 {
		t.Errorf("unknown entity: %d, want 404", w.Code)
	}
}

func TestBeliefsBlock(t *testing.T) {
	s, _ := newTestServer(t)

	kaelen := visibleText(do(t, s, "GET", "/entity/char_kaelen_vor", nil).Body.String())
	for _, want := range []string{"What Kaelen Vor believes", "Orrin sworn to The Ashen Court", "contradicts canon"} {
		if !strings.Contains(kaelen, want) {
			t.Errorf("Kaelen's page lacks %q", want)
		}
	}
	miren := visibleText(do(t, s, "GET", "/entity/char_miren", nil).Body.String())
	if !strings.Contains(miren, "Orrin sworn to The Ashen Court is false") || strings.Contains(miren, "contradicts canon") {
		t.Errorf("Miren holds canon's value: %q", miren)
	}
	place := visibleText(do(t, s, "GET", "/entity/loc_vale_of_orrin", nil).Body.String())
	if strings.Contains(place, "believes") {
		t.Errorf("a location holds no beliefs: %q", place)
	}
}

func TestBodyEditMakesDraft(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_miren"

	saved, err := s.draft(id)
	if err != nil {
		t.Fatal(err)
	}
	same, err := world.RawBody(saved.Base)
	if err != nil {
		t.Fatal(err)
	}
	// The saved prose, as a browser would post it, changes nothing.
	post := func(body string) {
		t.Helper()
		w := do(t, s, "POST", "/entity/"+id+"/body", url.Values{"body": {body}})
		if w.Code != 200 || w.Header().Get("HX-Trigger") != eventDirty {
			t.Fatalf("POST body: %d, trigger %q", w.Code, w.Header().Get("HX-Trigger"))
		}
	}
	post(strings.ReplaceAll(same, "\n", "\r\n"))
	if s.dirty(id) {
		t.Error("the saved prose made the entity dirty")
	}
	post("A new first line.\n\nAnd a second paragraph.")
	d, _ := s.draft(id)
	if d.Body == nil || *d.Body != "A new first line.\n\nAnd a second paragraph." || !s.dirty(id) {
		t.Fatalf("draft body = %v, dirty %v", d.Body, s.dirty(id))
	}
	if page := do(t, s, "GET", "/entity/"+id, nil).Body.String(); !strings.Contains(page, "And a second paragraph.") {
		t.Error("the page does not show the draft's prose")
	}
	post(same)
	if d, _ := s.draft(id); d.Body != nil {
		t.Error("restoring the saved prose left a draft body")
	}
}

func TestCreatedEntityPage(t *testing.T) {
	s, _ := newTestServer(t)
	err := s.editDraft("char_kaelen_vor", func(d *Draft) error {
		d.Created = append(d.Created, Created{ID: "char_abc123", Type: schema.TypeCharacter, Name: "Zed", File: "characters/zed.md"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "GET", "/entity/char_abc123", nil)
	if w.Code != 200 {
		t.Fatalf("created page: %d\n%s", w.Code, w.Body)
	}
	text := visibleText(w.Body.String())
	for _, want := range []string{"Zed", schema.TypeCharacter, "not saved yet: it is written when Kaelen Vor is saved"} {
		if !strings.Contains(text, want) {
			t.Errorf("created page lacks %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "<textarea") || strings.Contains(w.Body.String(), "lk-fields") {
		t.Error("a created entity has no form and no prose editing")
	}
}
