package editor

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/world"
)

// resolveFixture is the resolver's world, whose conditional entities show
// what a worldline hides.
var resolveFixture = filepath.Join("..", "..", "testdata", "world-resolve")

// newResolveServer starts an editor over a copy of world-resolve, as
// newTestServer does over world-ok.
func newResolveServer(t *testing.T) *Server {
	t.Helper()
	repo := t.TempDir()
	for _, sub := range []string{"schema", "world"} {
		if err := os.CopyFS(filepath.Join(repo, sub), os.DirFS(filepath.Join(resolveFixture, sub))); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Config{Repo: repo, Settings: filepath.Join(t.TempDir(), "editor.json")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.host = "127.0.0.1:4321"
	return s
}

// setWorldline selects a worldline in the settings, as Step 7's control will.
func setWorldline(s *Server, wl map[string]string) {
	s.prefs.mu.Lock()
	defer s.prefs.mu.Unlock()
	s.prefs.s.Worldline = wl
}

// panelHTML renders the relations panel of id with id as the only tab.
func panelHTML(t *testing.T, s *Server, id string) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.relationsPanel(id, Tabs{IDs: []string{id}, Active: id}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// draftEntity is the entity as its draft would leave it.
func draftEntity(t *testing.T, s *Server, id string) *world.Entity {
	t.Helper()
	d, err := s.draft(id)
	if err != nil {
		t.Fatal(err)
	}
	e, _, err := d.Entity()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestRelationsPanelGroupsByRole: membership first, coloured by role, the
// target by name, and derived edges read only with where they come from.
func TestRelationsPanelGroupsByRole(t *testing.T) {
	s, _ := newTestServer(t)

	got := panelHTML(t, s, "char_kaelen_vor")
	member := strings.Index(got, ">member_of<")
	other := strings.Index(got, ">originates_from<")
	if member < 0 || other < 0 || member > other {
		t.Fatalf("member_of should come before originates_from:\n%s", got)
	}
	for _, want := range []string{"rel-membership", "rel-other", ">The Ashen Court<", ">The Vale of Orrin<", ">The Siege of Vale<"} {
		if !strings.Contains(got, want) {
			t.Errorf("panel lacks %s", want)
		}
	}
	for _, id := range []string{">fac_ashen_court<", ">loc_vale_of_orrin<", ">evt_siege_of_vale<"} {
		if strings.Contains(got, id) {
			t.Errorf("panel shows the ID %s", id)
		}
	}

	court := panelHTML(t, s, "fac_ashen_court")
	if !strings.Contains(court, ">has_member<") || !strings.Contains(court, "derived: inverse of member_of") {
		t.Errorf("the court's panel lacks its derived members:\n%s", court)
	}
	// One authored relation, two derived: only the authored row edits.
	if n := strings.Count(court, "lk-rel-remove"); n != 1 || strings.Count(court, "lk-rel-priority") != 1 {
		t.Errorf("the court's panel has %d removable rows, want 1:\n%s", n, court)
	}
}

// TestRelationRowEdits: priority, note and remove land in the draft on the
// authored index, and mark the entity dirty.
func TestRelationRowEdits(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_kaelen_vor"
	post := func(path string, form url.Values) {
		t.Helper()
		w := do(t, s, "POST", "/entity/"+id+"/relations/"+path+"?tabs="+id+"&at="+id, form)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d\n%s", path, w.Code, w.Body)
		}
		if !strings.Contains(w.Header().Get("HX-Trigger"), eventDirty) {
			t.Errorf("%s: HX-Trigger %q, want %s", path, w.Header().Get("HX-Trigger"), eventDirty)
		}
		if !strings.Contains(w.Body.String(), `id="lk-rel-panel"`) {
			t.Errorf("%s: the answer is not the panel", path)
		}
	}

	post("1/priority", url.Values{"priority": {"4"}})
	post("1/note", url.Values{"note": {"born there"}})
	post("0/priority", url.Values{"priority": {""}})
	post("2/remove", nil)

	e := draftEntity(t, s, id)
	if len(e.Relations) != 2 {
		t.Fatalf("relations = %+v, want two after a remove", e.Relations)
	}
	if e.Relations[0].Priority != nil {
		t.Errorf("an empty priority kept the key: %d", *e.Relations[0].Priority)
	}
	if p := e.Relations[1].Priority; p == nil || *p != 4 || e.Relations[1].Note != "born there" {
		t.Errorf("relation 1 = %+v, want priority 4 and the note", e.Relations[1])
	}
	if !s.dirty(id) {
		t.Error("the entity is not dirty")
	}

	// A refused edit says why in the panel and opens nothing.
	w := do(t, s, "POST", "/entity/"+id+"/relations/9/remove", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "lk-rel-err") {
		t.Errorf("an out-of-range index: %d\n%s", w.Code, w.Body)
	}
	if strings.Contains(w.Header().Get("HX-Trigger"), eventDirty) {
		t.Error("a refused edit marked the entity dirty")
	}
}

// TestPagesReadThroughResolver: an entity absent under the worldline does
// not appear in the panel of an entity linked to it.
func TestPagesReadThroughResolver(t *testing.T) {
	s := newResolveServer(t)
	const keep = "Vale Keep"

	// Under held it is there: the test can see it go.
	setWorldline(s, map[string]string{"dec_vale": "held"})
	if got := panelHTML(t, s, "loc_vale"); !strings.Contains(got, keep) {
		t.Fatalf("under held, the Vale's panel lacks %s:\n%s", keep, got)
	}

	setWorldline(s, map[string]string{"dec_vale": "fell"})
	for _, id := range []string{"loc_vale", "obj_horn"} {
		if got := panelHTML(t, s, id); strings.Contains(got, keep) || strings.Contains(got, "loc_vale_keep") {
			t.Errorf("under fell, the panel of %s shows %s:\n%s", id, keep, got)
		}
		page := do(t, s, "GET", "/entity/"+id, nil)
		if page.Code != http.StatusOK || strings.Contains(page.Body.String(), keep) {
			t.Errorf("under fell, the page of %s: %d, shows %s", id, page.Code, keep)
		}
	}
}
