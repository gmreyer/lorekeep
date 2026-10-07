package editor

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/schema"
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

func mustRelation(t *testing.T, s *Server, name string) *schema.Relation {
	t.Helper()
	rel, ok := s.cur.pack.Relation(name)
	if !ok {
		t.Fatalf("the pack lacks %s", name)
	}
	return rel
}

// TestRelationsPanelGroupsByRole:membership first, coloured by role, the
// target by name, and derived edges read only with where they come from.
func TestRelationsPanelGroupsByRole(t *testing.T) {
	s, _ := newTestServer(t)
	// Relation names come from the fixture, never from a literal here.
	rels := draftEntity(t, s, "char_kaelen_vor").Relations
	membership, placement := rels[0].Type, rels[1].Type
	if rel, _ := s.cur.pack.Relation(membership); rel.Role != schema.RoleMembership {
		t.Fatalf("fixture: %s is not a membership relation", membership)
	}

	got := panelHTML(t, s, "char_kaelen_vor")
	member := strings.Index(got, ">"+membership+"<")
	other := strings.Index(got, ">"+placement+"<")
	if member < 0 || other < 0 || member > other {
		t.Fatalf("%s should come before %s:\n%s", membership, placement, got)
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
	inverse := mustRelation(t, s, membership).Inverse
	if !strings.Contains(court, ">"+inverse+"<") || !strings.Contains(court, "derived: inverse of "+membership) {
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
	// rowPath is a row edit as the panel posts it: the index and the
	// relation's identity, read from the draft as it stands.
	rowPath := func(i int, edit string) string {
		r := draftEntity(t, s, id).Relations[i]
		return "/entity/" + id + "/relations/" + strconv.Itoa(i) + "/" + edit + "?tabs=" + id + "&at=" + id +
			"&relation=" + r.Type + "&target=" + r.Target
	}
	post := func(i int, edit string, form url.Values) {
		t.Helper()
		w := do(t, s, "POST", rowPath(i, edit), form)
		if w.Code != http.StatusOK {
			t.Fatalf("%d/%s: %d\n%s", i, edit, w.Code, w.Body)
		}
		if !strings.Contains(w.Header().Get("HX-Trigger"), eventDirty) {
			t.Errorf("%d/%s: HX-Trigger %q, want %s\n%s", i, edit, w.Header().Get("HX-Trigger"), eventDirty, w.Body)
		}
		if !strings.Contains(w.Body.String(), `id="lk-rel-panel"`) {
			t.Errorf("%d/%s: the answer is not the panel", i, edit)
		}
	}

	post(1, "priority", url.Values{"priority": {"4"}})
	post(1, "note", url.Values{"note": {"born there"}})
	post(0, "priority", url.Values{"priority": {""}})
	stale := rowPath(1, "note") // as a panel rendered now would post it
	post(2, "remove", nil)

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

	// An edit naming a relation that is no longer at its index is refused.
	post(0, "remove", nil)
	before := draftEntity(t, s, id).Relations
	w = do(t, s, "POST", stale, url.Values{"note": {"lands nowhere"}})
	if !strings.Contains(w.Body.String(), "has moved") || strings.Contains(w.Header().Get("HX-Trigger"), eventDirty) {
		t.Errorf("a stale edit was not refused: %s\n%s", w.Header().Get("HX-Trigger"), w.Body)
	}
	if after := draftEntity(t, s, id).Relations; len(after) != len(before) || after[0].Note != before[0].Note {
		t.Errorf("a stale edit changed the draft: %+v", after)
	}
}

// TestAmbiguousRowsReadOnly: two relations to one target that differ only in
// valid_in, one of them hidden by the worldline, cannot be told apart from
// the resolver's edges, so the shown one is read only.
func TestAmbiguousRowsReadOnly(t *testing.T) {
	s, repo := newTestServer(t)
	const id = "char_kaelen_vor"
	rels := draftEntity(t, s, id).Relations
	membership, court := rels[0].Type, rels[0].Target
	d, err := s.draft("dec_siege_outcome")
	if err != nil {
		t.Fatal(err)
	}
	dec, _, _ := d.Entity()
	held, fell := dec.Outcomes[0], dec.Outcomes[1]

	file := filepath.Join(repo, "world", filepath.FromSlash(draftFile(t, s, id)))
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	extra := "  - { type: " + membership + ", target: " + court + ", valid_in: [{ decision: dec_siege_outcome, outcome: " + held + " }] }\n" +
		"  - { type: " + membership + ", target: " + court + ", valid_in: [{ decision: dec_siege_outcome, outcome: " + fell + " }] }\n"
	anchor := "relations:\n"
	src = []byte(strings.Replace(string(src), anchor, anchor+extra, 1))
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if fs, err := s.rebuild(); err != nil || fs.Errors() > 0 {
		t.Fatalf("rebuild: %v %v", err, fs)
	}
	setWorldline(s, map[string]string{"dec_siege_outcome": held})

	got := panelHTML(t, s, id)
	if n := strings.Count(got, ">"+membership+"<"); n != 2 {
		t.Fatalf("%d %s rows, want the unconditional one and one of the two:\n%s", n, membership, got)
	}
	if !strings.Contains(got, ambiguousNote) {
		t.Errorf("the ambiguous row does not say why it is read only:\n%s", got)
	}
	// The unconditional one (index 2 now) stays editable; neither
	// conditional one (0, 1) has an edit.
	if !strings.Contains(got, "relations/2/remove") {
		t.Errorf("the unambiguous row lost its edits:\n%s", got)
	}
	for _, i := range []string{"0", "1"} {
		if strings.Contains(got, "relations/"+i+"/") {
			t.Errorf("conditional row %s is editable:\n%s", i, got)
		}
	}
}

// draftFile is the world-relative file of id.
func draftFile(t *testing.T, s *Server, id string) string {
	t.Helper()
	d, err := s.draft(id)
	if err != nil {
		t.Fatal(err)
	}
	return d.File
}

// TestReplayRelations: the draft's changes to the list map each item back
// to its place in the base file, or mark it new.
func TestReplayRelations(t *testing.T) {
	rel := func(p ...string) []string { return append([]string{"relations"}, p...) }
	changes := []world.Change{
		{Path: rel("1"), Delete: true},
		{Path: rel("-"), Value: world.Relation{}},
		{Path: rel("0", "note"), Value: "x"},
		{Path: []string{"name"}, Value: "y"},
		{Path: rel("2"), Delete: true},
		{Path: rel("-"), Value: world.Relation{}},
	}
	if got, want := replayRelations(4, changes), []int{0, 2, newRow, newRow}; !slices.Equal(got, want) {
		t.Errorf("replay = %v, want %v", got, want)
	}
	if got := replayRelations(2, []world.Change{{Path: rel(), Delete: true}, {Path: rel("-")}}); !slices.Equal(got, []int{newRow}) {
		t.Errorf("replay after deleting the list = %v", got)
	}
}

// TestTakenIDsCoverEverySource: entities and statements in the index, and
// entities created in any open draft, are all taken.
func TestTakenIDsCoverEverySource(t *testing.T) {
	s, _ := newTestServer(t)
	s.drafts.mu.Lock()
	s.drafts.m["char_miren"] = &Draft{ID: "char_miren", Created: []Created{{ID: "char_other1"}}}
	s.drafts.mu.Unlock()
	d := &Draft{ID: "char_kaelen_vor", Created: []Created{{ID: "char_mine01"}}}

	var ids []string
	s.drafts.mu.Lock()
	err := s.read(func(cur *loaded) error {
		ids = s.takenIDs(cur, d)
		return nil
	})
	s.drafts.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var stmt string
	_ = s.read(func(cur *loaded) error { stmt = cur.idx.Statements[0].ID; return nil })
	taken := idSet(ids)
	for _, want := range []string{"char_kaelen_vor", "LOC_VALE_OF_ORRIN", stmt, "char_other1", "Char_Mine01"} {
		if !taken(want) {
			t.Errorf("%s is not taken", want)
		}
	}
	if taken("char_free00") {
		t.Error("an unused ID is taken")
	}
}

// TestPagesReadThroughResolver: an entity absent under the worldline does
// not appear in the panel of an entity linked to it.
func TestPagesReadThroughResolver(t *testing.T) {
	s := newResolveServer(t)
	const keep = "Vale Keep"

	search := func() string {
		t.Helper()
		w := do(t, s, "GET", "/entity/loc_vale/relations/search?tabs=loc_vale&at=loc_vale&q=keep", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("search: %d\n%s", w.Code, w.Body)
		}
		return w.Body.String()
	}

	// Under held it is there: the test can see it go.
	setWorldline(s, map[string]string{"dec_vale": "held"})
	if got := panelHTML(t, s, "loc_vale"); !strings.Contains(got, keep) {
		t.Fatalf("under held, the Vale's panel lacks %s:\n%s", keep, got)
	}
	if got := search(); !strings.Contains(got, ">"+keep+"<") {
		t.Fatalf("under held, the search lacks %s:\n%s", keep, got)
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
	// The search still offers to create it: the typed text, not the entity.
	if got := search(); strings.Contains(got, ">"+keep+"<") || strings.Contains(got, "loc_vale_keep") {
		t.Errorf("under fell, the search finds %s:\n%s", keep, got)
	}

	// With a draft holding changes, the hidden relation stays hidden: the
	// horn gains a link to the Vale, and an edit reaches its hidden one.
	const horn = "obj_horn"
	hornType, valeType := draftEntity(t, s, horn).Type, draftEntity(t, s, "loc_vale").Type
	rel := validRelations(s.cur.pack, hornType, valeType)[0].Name
	w := do(t, s, "POST", "/entity/"+horn+"/relations/link?tabs="+horn+"&at="+horn+"&target=loc_vale&relation="+rel, nil)
	if !s.dirty(horn) {
		t.Fatalf("the link was not added:\n%s", w.Body)
	}
	if err := s.editDraft(horn, func(d *Draft) error {
		d.Changes = append(d.Changes, world.Change{Path: []string{"relations", "0", "note"}, Value: "hidden"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := panelHTML(t, s, horn)
	if strings.Contains(got, keep) || strings.Contains(got, "not present") || strings.Contains(got, "hidden") {
		t.Errorf("with a draft, the horn's panel shows its hidden relation:\n%s", got)
	}
	if !strings.Contains(got, ">The Vale<") || !strings.Contains(got, "relations/1/remove") {
		t.Errorf("the added relation is not row 1 of the draft:\n%s", got)
	}
}

// TestPanelErrorHidesID: a panel that cannot be read says so in plain
// words, never with the ID.
func TestPanelErrorHidesID(t *testing.T) {
	s := newResolveServer(t)
	setWorldline(s, map[string]string{"dec_vale": "fell"})
	got := panelHTML(t, s, "loc_vale_keep")
	if strings.Contains(got, "loc_vale_keep") || !strings.Contains(got, "not in the current branch") {
		t.Errorf("error panel:\n%s", got)
	}
}

// relNames are the relation names a picker answer offers, in order.
func relNames(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`class="lk-rel-name [^"]*">([^<]+)<`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestPickerOffersOnlyValidRelations: picking a target whose type admits
// several relations asks with exactly the ones the pack allows.
func TestPickerOffersOnlyValidRelations(t *testing.T) {
	s, _ := newTestServer(t)
	const src, dst = "char_kaelen_vor", "fac_ashen_court"
	pack := s.cur.pack

	var want []string
	for _, rel := range pack.Relations {
		if pack.AllowsDomain(rel.Name, schema.TypeCharacter) && pack.AllowsRange(rel.Name, schema.TypeFaction) {
			want = append(want, rel.Name)
		}
	}
	if len(want) < 2 {
		t.Fatalf("fixture: %v, want a pair with several relations", want)
	}
	// A known absence: a relation that cannot start at a character.
	var absent string
	for _, rel := range pack.Relations {
		if !pack.AllowsDomain(rel.Name, schema.TypeCharacter) && pack.AllowsRange(rel.Name, schema.TypeFaction) {
			absent = rel.Name
			break
		}
	}
	if absent == "" {
		t.Fatal("fixture: no relation into a faction that a character cannot hold")
	}

	hits := do(t, s, "GET", "/entity/"+src+"/relations/search?tabs="+src+"&at="+src+"&q=ashen", nil)
	if !strings.Contains(hits.Body.String(), ">The Ashen Court<") || strings.Contains(hits.Body.String(), ">"+dst+"<") {
		t.Fatalf("search: want the court by name, never its ID:\n%s", hits.Body)
	}

	w := do(t, s, "POST", "/entity/"+src+"/relations/link?tabs="+src+"&at="+src+"&target="+dst, nil)
	if w.Code != http.StatusOK || w.Header().Get("HX-Retarget") != "#lk-rel-hits" {
		t.Fatalf("pick: %d, retarget %q\n%s", w.Code, w.Header().Get("HX-Retarget"), w.Body)
	}
	if got := relNames(w.Body.String()); !slices.Equal(got, want) {
		t.Errorf("offered %v, want %v", got, want)
	}
	if strings.Contains(w.Body.String(), ">"+absent+"<") {
		t.Errorf("offered %s, which a character cannot hold", absent)
	}
	for _, c := range validRelations(pack, schema.TypeCharacter, schema.TypeFaction) {
		rel := mustRelation(t, s, c.Name)
		if !strings.Contains(w.Body.String(), "lk-rel-name "+relClass(rel.Role)+`">`+c.Name+"<") || c.Role != roleLabel(rel.Role) {
			t.Errorf("%s is not coloured and labelled by its role %q", c.Name, rel.Role)
		}
	}
	if s.dirty(src) {
		t.Error("asking which relation added one")
	}

	// A relation the pack does not allow is refused, even when asked for.
	w = do(t, s, "POST", "/entity/"+src+"/relations/link?tabs="+src+"&at="+src+"&target="+dst+"&relation="+absent, nil)
	if !strings.Contains(w.Body.String(), "lk-rel-err") || s.dirty(src) {
		t.Errorf("asking for %s was not refused:\n%s", absent, w.Body)
	}

	// Choosing one adds it.
	w = do(t, s, "POST", "/entity/"+src+"/relations/link?tabs="+src+"&at="+src+"&target="+dst+"&relation="+want[len(want)-1], nil)
	rels := draftEntity(t, s, src).Relations
	if last := rels[len(rels)-1]; last.Type != want[len(want)-1] || last.Target != dst {
		t.Errorf("added %+v\n%s", last, w.Body)
	}
}

// TestNoValidRelationGreysOut: a hit no relation reaches is shown greyed,
// and picking it anyway is refused. The core pack's soft link runs between
// any two types, so this is checked on the pieces.
func TestNoValidRelationGreysOut(t *testing.T) {
	const src, dst = schema.TypeEvent, schema.TypeCharacter
	want := "no relation between " + src + " and " + dst
	if _, err := pickRelation(nil, "", src, dst); err == nil || err.Error() != want {
		t.Errorf("pickRelation with none: %v", err)
	}
	var b bytes.Buffer
	m := relSearch{ID: "a", Query: "b", SrcType: src, Types: []string{dst},
		Hits: []relHit{{ID: "b_id", Name: "Bee", Type: dst}}}
	if err := relHits(m).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), want) ||
		strings.Contains(b.String(), "target=b_id") || strings.Contains(b.String(), "b_id") {
		t.Errorf("a hit with no relation:\n%s", b.String())
	}
}

// TestSingleValidRelationAutoPicked: a pair with exactly one valid relation
// adds it on the pick, with no second request.
func TestSingleValidRelationAutoPicked(t *testing.T) {
	s, _ := newTestServer(t)
	const src, dst = "loc_vale_of_orrin", "char_miren"
	only := validRelations(s.cur.pack, draftEntity(t, s, src).Type, schema.TypeCharacter)
	if len(only) != 1 {
		t.Fatalf("fixture: location → character allows %v, want one", only)
	}

	w := do(t, s, "POST", "/entity/"+src+"/relations/link?tabs="+src+"&at="+src+"&target="+dst, nil)
	if w.Code != http.StatusOK || w.Header().Get("HX-Retarget") != "" {
		t.Fatalf("pick: %d, retarget %q\n%s", w.Code, w.Header().Get("HX-Retarget"), w.Body)
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), eventDirty) {
		t.Errorf("HX-Trigger %q, want %s", w.Header().Get("HX-Trigger"), eventDirty)
	}
	rels := draftEntity(t, s, src).Relations
	if len(rels) != 1 || rels[0].Type != only[0].Name || rels[0].Target != dst {
		t.Fatalf("relations = %+v, want one %s to %s", rels, only[0].Name, dst)
	}
	// It lands in the panel as an authored row, by name.
	if !strings.Contains(w.Body.String(), ">Miren<") || !strings.Contains(w.Body.String(), "relations/0/remove") {
		t.Errorf("the panel lacks the new row:\n%s", w.Body)
	}
	d, _ := s.draft(src)
	if c := d.Changes[0]; !slices.Equal(c.Path, []string{"relations", "-"}) {
		t.Errorf("change path %v, want relations.-", c.Path)
	} else if _, ok := c.Value.(world.Relation); !ok {
		t.Errorf("change value %T, want a struct", c.Value)
	}
}

// TestCreateAndLinkSaveTogether: a create puts the new file and the relation
// naming it into one draft, which is what the save writes together.
func TestCreateAndLinkSaveTogether(t *testing.T) {
	s, _ := newTestServer(t)
	const src, name = "char_kaelen_vor", "Ser Aldric"
	base := "/entity/" + src + "/relations/"
	tabsQ := "tabs=" + src + "&at=" + src

	// The search offers the create, with a type to choose: a character's
	// relations reach several types.
	hits := do(t, s, "GET", base+"search?"+tabsQ+"&q="+url.QueryEscape(name), nil)
	types := targetTypes(s.cur.pack, schema.TypeCharacter)
	if len(types) < 2 || !strings.Contains(hits.Body.String(), "Create “"+name+"”") || !strings.Contains(hits.Body.String(), `<select name="type"`) {
		t.Fatalf("search, types %v:\n%s", types, hits.Body)
	}
	for _, typ := range types {
		if !strings.Contains(hits.Body.String(), `<option value="`+typ+`">`) {
			t.Errorf("the type dropdown lacks %s", typ)
		}
	}

	// A character to a character allows several relations: it asks first.
	form := url.Values{"name": {name}, "type": {schema.TypeCharacter}}
	w := do(t, s, "POST", base+"create?"+tabsQ, form)
	choices := validRelations(s.cur.pack, schema.TypeCharacter, schema.TypeCharacter)
	if w.Header().Get("HX-Retarget") != "#lk-rel-hits" || len(relNames(w.Body.String())) != len(choices) {
		t.Fatalf("create asked %v, want %d choices\n%s", relNames(w.Body.String()), len(choices), w.Body)
	}
	if s.dirty(src) {
		t.Fatal("asking which relation created something")
	}

	rel := choices[0].Name
	w = do(t, s, "POST", base+"create?"+tabsQ+"&relation="+rel, form)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d\n%s", w.Code, w.Body)
	}
	d, err := s.draft(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Created) != 1 {
		t.Fatalf("created = %+v, want one", d.Created)
	}
	c := d.Created[0]
	prefix, _ := s.cur.pack.IDPrefix(schema.TypeCharacter)
	if !regexp.MustCompile(`^`+prefix+`_[a-z0-9]{6}$`).MatchString(c.ID) || strings.Contains(c.ID, "aldric") {
		t.Errorf("created ID %q", c.ID)
	}
	if c.File != "characters/"+c.ID+".md" || c.Name != name || c.Type != schema.TypeCharacter {
		t.Errorf("created = %+v, want it beside the other characters", c)
	}

	// The draft's file names it ...
	e := draftEntity(t, s, src)
	if last := e.Relations[len(e.Relations)-1]; last.Type != rel || last.Target != c.ID {
		t.Errorf("last relation %+v, want %s to %s", last, rel, c.ID)
	}
	// ... and its content is a draft entity of that ID, type and name.
	doc, fs := world.Parse(c.File, c.Content)
	if len(fs) > 0 || doc.Entity == nil {
		t.Fatalf("created content does not parse: %v\n%s", fs, c.Content)
	}
	ne := doc.Entity
	if ne.ID != c.ID || ne.Type != schema.TypeCharacter || ne.Name != name || ne.Status != world.StatusDraft ||
		ne.Visibility.Kind != world.VisibilityInternal || ne.HasBody() {
		t.Errorf("created entity = %+v", ne)
	}
	wantContent := "---\nid: " + c.ID + "\ntype: character\nname: " + name + "\nstatus: draft\nvisibility: internal\n---\n"
	if string(c.Content) != wantContent {
		t.Errorf("content:\n%s\nwant:\n%s", c.Content, wantContent)
	}

	// It opens in a background tab; the writer stays where they were.
	if u := w.Header().Get("HX-Replace-Url"); u != "/entity/"+src+"?tabs="+url.QueryEscape(src+","+c.ID) {
		t.Errorf("HX-Replace-Url %q", u)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="lk-tabs"`) || !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, `id="lk-rel-panel"`) {
		t.Errorf("the answer lacks the panel or the out-of-band tab strip:\n%s", body)
	}
	if !strings.Contains(body, ">"+name+"<") {
		t.Errorf("the panel does not name the created entity:\n%s", body)
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), eventDirty) {
		t.Errorf("HX-Trigger %q", w.Header().Get("HX-Trigger"))
	}

	// A second create never takes an ID an open draft holds.
	w = do(t, s, "POST", base+"create?"+tabsQ+"&relation="+rel, form)
	d, _ = s.draft(src)
	if len(d.Created) != 2 || strings.EqualFold(d.Created[0].ID, d.Created[1].ID) {
		t.Errorf("second create: %+v", d.Created)
	}
}

// TestCreateFolderForNewType: a type with no entities yet goes in a folder
// named for it.
func TestCreateFolderForNewType(t *testing.T) {
	s, _ := newTestServer(t)
	const src = "char_kaelen_vor"
	// A type no entity of the fixture has, that a character can reach.
	used := map[string]bool{}
	_ = s.read(func(cur *loaded) error {
		ents, err := cur.res.Entities(s.readContext())
		for _, e := range ents {
			used[e.Type] = true
		}
		return err
	})
	var typ string
	for _, tt := range targetTypes(s.cur.pack, schema.TypeCharacter) {
		if !used[tt] {
			typ = tt
			break
		}
	}
	if typ == "" {
		t.Fatal("fixture: every type has an entity")
	}
	rel := validRelations(s.cur.pack, schema.TypeCharacter, typ)[0].Name
	w := do(t, s, "POST", "/entity/"+src+"/relations/create?tabs="+src+"&at="+src,
		url.Values{"name": {"Old Tongue"}, "type": {typ}, "relation": {rel}})
	d, _ := s.draft(src)
	if len(d.Created) != 1 {
		t.Fatalf("no create:\n%s", w.Body)
	}
	if c := d.Created[0]; c.File != typ+"/"+c.ID+".md" {
		t.Errorf("file %q, want %s/<id>.md", c.File, typ)
	}
}

// TestNewIDOpaqueAndUnique: prefix, underscore, six [a-z0-9]; a taken ID,
// even one differing only in case, forces a retry.
func TestNewIDOpaqueAndUnique(t *testing.T) {
	shape := regexp.MustCompile(`^char_[a-z0-9]{6}$`)
	var tried []string
	taken := func(id string) bool {
		tried = append(tried, id)
		switch len(tried) {
		case 1:
			// Taken by an ID that differs only in case.
			return idSet([]string{strings.ToUpper(id)})(id)
		case 2:
			return true
		}
		return false
	}
	id, err := newID("char", taken)
	if err != nil {
		t.Fatal(err)
	}
	if len(tried) != 3 || id != tried[2] {
		t.Fatalf("tried %v, got %q: want the third candidate", tried, id)
	}
	for _, c := range tried {
		if !shape.MatchString(c) {
			t.Errorf("candidate %q is not char_ and six [a-z0-9]", c)
		}
	}

	if _, err := newID("char", func(string) bool { return true }); err == nil {
		t.Error("an ID came back with every candidate taken")
	}
	seen := map[string]bool{}
	for range 200 {
		id, _ := newID("x", func(string) bool { return false })
		seen[id] = true
	}
	if len(seen) < 199 {
		t.Errorf("200 IDs, %d distinct", len(seen))
	}
}

// TestMatchEntities: names and aliases, ignoring case, prefixes first, the
// entity itself left out, at most maxHits.
func TestMatchEntities(t *testing.T) {
	ents := []resolve.Summary{
		{ID: "a", Name: "The Vale"},
		{ID: "b", Name: "Valewood"},
		{ID: "c", Name: "Kaelen", Aliases: []string{"vale warden"}},
		{ID: "d", Name: "Orrin"},
		{ID: "self", Name: "Vale Keep"},
	}
	var got []string
	for _, e := range matchEntities(ents, "self", "VALE") {
		got = append(got, e.ID)
	}
	if want := []string{"c", "b", "a"}; !slices.Equal(got, want) {
		t.Errorf("matches %v, want %v", got, want)
	}
	var many []resolve.Summary
	for i := range 30 {
		many = append(many, resolve.Summary{ID: strconv.Itoa(i), Name: "x"})
	}
	if n := len(matchEntities(many, "", "x")); n != maxHits {
		t.Errorf("%d hits, want %d", n, maxHits)
	}
}
