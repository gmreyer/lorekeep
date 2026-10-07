package editor

import (
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

var dataField = regexp.MustCompile(`data-field="([^"]+)"`)

func rowsOf(page string) []string {
	var out []string
	for _, m := range dataField.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

// entityOf parses the entity as the draft of id would leave it.
func entityOf(t *testing.T, s *Server, id string) *world.Entity {
	t.Helper()
	d, err := s.draft(id)
	if err != nil {
		t.Fatal(err)
	}
	e, _, err := d.Entity()
	if err != nil {
		t.Fatal(err)
	}
	e.Source, e.Body = world.Source{}, ""
	return e
}

func postField(t *testing.T, s *Server, id, field string, kv ...string) string {
	t.Helper()
	f := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Add(kv[i], kv[i+1])
	}
	w := do(t, s, "POST", "/entity/"+id+"/fields/"+field, f)
	if w.Code != 200 {
		t.Fatalf("POST %s %s %v: %d %s", id, field, kv, w.Code, w.Body)
	}
	if w.Header().Get("HX-Trigger") != eventDirty {
		t.Errorf("POST %s %s: HX-Trigger %q", id, field, w.Header().Get("HX-Trigger"))
	}
	return w.Body.String()
}

func TestFieldsFormRowsForType(t *testing.T) {
	s, _ := newTestServer(t)
	ents := entitiesOf(t, s)
	seen := map[string]bool{}
	for _, e := range ents {
		w := do(t, s, "GET", "/entity/"+e.ID+"/fields", nil)
		if w.Code != 200 {
			t.Fatalf("fields of %s: %d", e.ID, w.Code)
		}
		want := []string{"name", "aliases", "status", "visibility"}
		switch e.Type {
		case schema.TypeCharacter:
			want = append(want, "lifespan")
		case schema.TypeEvent:
			want = append(want, "date")
		case schema.TypeDecision:
			want = append(want, "outcomes")
		}
		want = append(want, "valid_in")
		if got := rowsOf(w.Body.String()); !slices.Equal(got, want) {
			t.Errorf("%s (%s) rows = %v, want %v", e.ID, e.Type, got, want)
		}
		seen[e.Type] = true
	}
	for _, typ := range []string{schema.TypeCharacter, schema.TypeEvent, schema.TypeDecision} {
		if !seen[typ] {
			t.Errorf("the fixture has no %s to check", typ)
		}
	}
}

func TestUnknownFieldKept(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_miren"
	const scalar, list = "future_key: kept as written", "future_list: [1, 2]"
	err := s.editDraft(id, func(d *Draft) error {
		d.Base = []byte(strings.Replace(string(d.Base), "status: canon\n", "status: canon\n"+scalar+"\n"+list+"\n", 1))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page := do(t, s, "GET", "/entity/"+id+"/fields", nil).Body.String()
	for _, want := range []string{`data-field="future_key"`, `data-field="future_list"`, "kept as written", "[1, 2]"} {
		if !strings.Contains(page, want) {
			t.Errorf("the form does not show %q", want)
		}
	}
	// No control posts to an unknown field.
	if strings.Contains(page, "/fields/future_key") {
		t.Error("an unknown field is editable")
	}
	if w := do(t, s, "POST", "/entity/"+id+"/fields/future_key", url.Values{"value": {"x"}}); w.Code != 400 {
		t.Errorf("editing an unknown field: %d, want 400", w.Code)
	}

	postField(t, s, id, "name", "value", "Miren of the Court")
	d, _ := s.draft(id)
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{scalar, list, "name: Miren of the Court"} {
		if !strings.Contains(string(out), line+"\n") {
			t.Errorf("the draft's file lacks the line %q:\n%s", line, out)
		}
	}
	// Only the name differs from before the edit.
	baseDoc, _ := world.Parse(d.File, d.Base)
	want := baseDoc.Entity
	want.Name = "Miren of the Court"
	want.Source, want.Body = world.Source{}, ""
	if got := entityOf(t, s, id); !reflect.DeepEqual(got, want) {
		t.Errorf("an edit of name changed more than name:\n%s", out)
	}
}

func TestFieldEdits(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_kaelen_vor"
	orig := entityOf(t, s, id)

	// name: the response carries the new title out of band.
	if page := postField(t, s, id, "name", "value", "Kaelen the Warden"); !strings.Contains(page, `hx-swap-oob="true"`) ||
		!strings.Contains(page, "Kaelen the Warden") {
		t.Error("a rename does not refresh the title")
	}

	// lists: add, a duplicate, remove (the last removal drops the key).
	postField(t, s, id, "aliases", "op", "add", "value", "The Warden")
	postField(t, s, id, "aliases", "op", "add", "value", "The Warden")
	postField(t, s, id, "aliases", "op", "add", "value", "   ")
	if got := entityOf(t, s, id).Aliases; !slices.Equal(got, []string{"The Ashen Knight", "Vor", "The Warden"}) {
		t.Errorf("aliases = %v", got)
	}
	postField(t, s, id, "aliases", "op", "remove", "value", "The Ashen Knight")
	postField(t, s, id, "aliases", "op", "remove", "value", "The Ashen Knight") // a stale second click
	if got := entityOf(t, s, id).Aliases; !slices.Equal(got, []string{"Vor", "The Warden"}) {
		t.Errorf("aliases after a doubled removal = %v", got)
	}
	postField(t, s, id, "aliases", "op", "remove", "value", "Vor")
	postField(t, s, id, "aliases", "op", "remove", "value", "The Warden")
	if got := entityOf(t, s, id).Aliases; len(got) != 0 {
		t.Errorf("aliases after removing all = %v", got)
	}
	if w := do(t, s, "POST", "/entity/"+id+"/fields/aliases", url.Values{"op": {"clear"}}); w.Code != 400 {
		t.Errorf("an unknown op: %d", w.Code)
	}

	// closed sets.
	postField(t, s, id, "status", "value", "draft")
	postField(t, s, id, "visibility", "value", "spoiler")
	if e := entityOf(t, s, id); e.Status != world.StatusDraft || e.Visibility.Kind != world.VisibilitySpoiler || e.Visibility.Act == "" {
		t.Errorf("status %q visibility %+v", e.Status, e.Visibility)
	}
	postField(t, s, id, "visibility", "sub", "act", "value", "act2")
	if e := entityOf(t, s, id); e.Visibility.Raw != "spoiler:act2" {
		t.Errorf("visibility = %q", e.Visibility.Raw)
	}
	postField(t, s, id, "visibility", "value", "internal")
	if w := do(t, s, "POST", "/entity/"+id+"/fields/status", url.Values{"value": {"nonsense"}}); w.Code != 400 {
		t.Errorf("a status outside the closed set: %d", w.Code)
	}

	// interval: clearing "to" deletes latest; clearing everything drops the key.
	postField(t, s, id, "lifespan", "sub", "latest", "value", "")
	if iv := entityOf(t, s, id).Lifespan; iv == nil || iv.Latest != nil || iv.Earliest == nil || *iv.Earliest != 380 {
		t.Errorf("lifespan after clearing to = %+v", iv)
	}
	postField(t, s, id, "lifespan", "sub", "precision", "value", "approximate")
	if iv := entityOf(t, s, id).Lifespan; iv.Precision != world.PrecisionApproximate {
		t.Errorf("precision = %q", iv.Precision)
	}
	postField(t, s, id, "lifespan", "sub", "precision", "value", "approximate") // again: clears
	if iv := entityOf(t, s, id).Lifespan; iv.Precision != "" {
		t.Errorf("precision after the second click = %q", iv.Precision)
	}
	postField(t, s, id, "lifespan", "sub", "latest", "value", "431")
	if iv := entityOf(t, s, id).Lifespan; iv.Latest == nil || *iv.Latest != 431 {
		t.Errorf("latest = %+v", iv)
	}
	for _, bad := range [][]string{{"sub", "era", "value", "no_such_era"}, {"sub", "earliest", "value", "x"}} {
		f := url.Values{bad[0]: {bad[1]}, bad[2]: {bad[3]}}
		if w := do(t, s, "POST", "/entity/"+id+"/fields/lifespan", f); w.Code != 400 {
			t.Errorf("lifespan %v: %d", bad, w.Code)
		}
	}
	postField(t, s, id, "lifespan", "sub", "era", "value", "")
	postField(t, s, id, "lifespan", "sub", "earliest", "value", "")
	postField(t, s, id, "lifespan", "sub", "latest", "value", "")
	if iv := entityOf(t, s, id).Lifespan; iv != nil {
		t.Errorf("an emptied lifespan stays: %+v", iv)
	}

	// A date belongs to an event, not to a character.
	if w := do(t, s, "POST", "/entity/"+id+"/fields/date", url.Values{"sub": {"earliest"}, "value": {"1"}}); w.Code != 400 {
		t.Errorf("date on a character: %d", w.Code)
	}

	// What was not edited is unchanged.
	got := entityOf(t, s, id)
	if !reflect.DeepEqual(got.Relations, orig.Relations) || !reflect.DeepEqual(got.Beliefs, orig.Beliefs) {
		t.Error("field edits touched relations or beliefs")
	}
}

func TestFieldErrorsShown(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_miren"
	postField(t, s, id, "name", "value", "")
	page := do(t, s, "GET", "/entity/"+id+"/fields", nil).Body.String()
	// A draft with no name is wrong in the way the validator says; the parser
	// alone does not know it, so this checks only that the row renders.
	if !strings.Contains(page, `data-field="name"`) {
		t.Error("no name row")
	}

	// A field that does not parse shows the parser's message under its row.
	err := s.editDraft(id, func(d *Draft) error {
		d.Base = []byte(strings.Replace(string(d.Base), "status: canon\n", "status: canon\nsurprise: 1\n", 1))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page = do(t, s, "GET", "/entity/"+id+"/fields", nil).Body.String()
	if !strings.Contains(page, `class="lk-row lk-bad" data-field="surprise"`) || !strings.Contains(page, "lk-ferr") {
		t.Errorf("no error under the unknown field:\n%s", page)
	}
}

func postCond(t *testing.T, s *Server, id string, kv ...string) string {
	t.Helper()
	f := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Add(kv[i], kv[i+1])
	}
	w := do(t, s, "POST", "/entity/"+id+"/conditions", f)
	if w.Code != 200 {
		t.Fatalf("POST conditions %v: %d %s", kv, w.Code, w.Body)
	}
	return w.Body.String()
}

// TestConditionChipEditsValidIn: adding and removing a condition changes
// exactly the valid_in list at the path and nothing else.
func TestConditionChipEditsValidIn(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_kaelen_vor"
	const dec = "dec_siege_outcome"
	orig := entityOf(t, s, id)

	// the root list, which is `valid_in: null` in the file.
	page := postCond(t, s, id, "path", "valid_in", "op", "add", "decision", dec, "outcome", "held")
	if !strings.Contains(page, "only when") || !strings.Contains(page, "The outcome of the Siege of Vale") || strings.Contains(page, dec) && strings.Contains(visibleText(page), dec) {
		t.Errorf("chips: %s", page)
	}
	want := *orig
	want.ValidIn = []world.Condition{{Decision: dec, Outcome: "held"}}
	if got := entityOf(t, s, id); !reflect.DeepEqual(*got, want) {
		t.Errorf("after add: %+v", got.ValidIn)
	}
	// a duplicate adds nothing.
	postCond(t, s, id, "path", "valid_in", "op", "add", "decision", dec, "outcome", "held")
	postCond(t, s, id, "path", "valid_in", "op", "add", "decision", dec, "outcome", "fell")
	if got := entityOf(t, s, id).ValidIn; len(got) != 2 {
		t.Errorf("two conditions expected (all must hold): %v", got)
	}
	postCond(t, s, id, "path", "valid_in", "op", "remove", "decision", dec, "outcome", "held")
	if got := entityOf(t, s, id).ValidIn; !slices.Equal(got, []world.Condition{{Decision: dec, Outcome: "fell"}}) {
		t.Errorf("after removing the first: %v", got)
	}
	postCond(t, s, id, "path", "valid_in", "op", "remove", "decision", dec, "outcome", "fell")
	if got := entityOf(t, s, id); !reflect.DeepEqual(*got, *orig) {
		t.Errorf("after removing the last the entity differs: %+v", got)
	}

	// a relation's list.
	postCond(t, s, id, "path", "relations.1.valid_in", "op", "add", "decision", dec, "outcome", "betrayed")
	want = *orig
	want.Relations = slices.Clone(orig.Relations)
	want.Relations[1].ValidIn = []world.Condition{{Decision: dec, Outcome: "betrayed"}}
	if got := entityOf(t, s, id); !reflect.DeepEqual(*got, want) {
		t.Errorf("relation after add: %+v", got.Relations)
	}
	postCond(t, s, id, "path", "relations.1.valid_in", "op", "remove", "decision", dec, "outcome", "betrayed")
	if got := entityOf(t, s, id); !reflect.DeepEqual(*got, *orig) {
		t.Errorf("relation after remove: %+v", got.Relations)
	}

	// an item that already has conditions keeps them when one is removed.
	const orrin = "char_orrin"
	oo := entityOf(t, s, orrin)
	postCond(t, s, orrin, "path", "relations.1.valid_in", "op", "add", "decision", dec, "outcome", "held")
	if got := entityOf(t, s, orrin).Relations[1].ValidIn; len(got) != 2 {
		t.Fatalf("orrin relation conditions: %v", got)
	}
	postCond(t, s, orrin, "path", "relations.1.valid_in", "op", "remove", "decision", dec, "outcome", "held")
	if got := entityOf(t, s, orrin); !reflect.DeepEqual(*got, *oo) {
		t.Errorf("orrin differs after add and remove: %+v", got.Relations)
	}
}

func TestConditionRouteRefusals(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_kaelen_vor"
	for name, kv := range map[string]map[string]string{
		"a path that is not a valid_in":  {"path": "relations.0.target"},
		"a nested path":                  {"path": "relations.0.valid_in.0"},
		"an index outside the entity":    {"path": "relations.9.valid_in"},
		"the type":                       {"path": "type"},
		"a dotted escape":                {"path": "valid_in..x"},
		"a decision that does not exist": {"path": "valid_in", "decision": "char_miren", "outcome": "held"},
		"an outcome the decision lacks":  {"path": "valid_in", "decision": "dec_siege_outcome", "outcome": "nope"},
		"a belief that is not there":     {"path": "beliefs.1.valid_in"},
		"an assert that is not there":    {"path": "asserts.0.valid_in"},
		"a belief item itself":           {"path": "beliefs.0"},
		"an unknown op":                  {"path": "valid_in", "op": "clear"},
	} {
		f := url.Values{}
		for k, v := range kv {
			f.Set(k, v)
		}
		if _, ok := kv["op"]; !ok {
			f.Set("op", "add")
		}
		if w := do(t, s, "POST", "/entity/"+id+"/conditions", f); w.Code != 400 {
			t.Errorf("%s: %d, want 400", name, w.Code)
		}
	}
	if s.dirty(id) {
		t.Error("a refused edit made a draft")
	}
	if w := do(t, s, "GET", "/entity/"+id+"/conditions?path=valid_in", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "+ only when…") {
		t.Errorf("chips: %d %s", w.Code, w.Body)
	}
	if w := do(t, s, "GET", "/entity/"+id+"/conditions/pick?path=valid_in", nil); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "The outcome of the Siege of Vale") || !strings.Contains(w.Body.String(), ">betrayed<") {
		t.Errorf("picker: %d %s", w.Code, w.Body)
	}
}

// TestConditionChipsStandalone: a caller with no names (Task 3's relation
// rows) gets chips that fetch their own.
func TestConditionChipsStandalone(t *testing.T) {
	var sb strings.Builder
	chips := conditionChips(CondTarget{
		Entity: "char_orrin", Path: []string{"relations", "1", "valid_in"},
		Conds: []world.Condition{{Decision: "dec_siege_outcome", Outcome: "betrayed"}},
	})
	if err := chips.Render(t.Context(), &sb); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="lk-cond-char_orrin-relations-1-valid_in"`, `hx-trigger="load"`, `hx-target="this"`, "/entity/char_orrin/conditions?path=relations.1.valid_in"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("lacks %q: %s", want, sb.String())
		}
	}
}

// A × clicked from a stale page names what it removes, so it cannot remove
// another condition, and a second click on it changes nothing.
func TestStaleRemovalsHitNothingElse(t *testing.T) {
	s, _ := newTestServer(t)
	const id, dec = "char_miren", "dec_siege_outcome"
	postCond(t, s, id, "path", "valid_in", "op", "add", "decision", dec, "outcome", "held")
	postCond(t, s, id, "path", "valid_in", "op", "add", "decision", dec, "outcome", "fell")
	postCond(t, s, id, "path", "valid_in", "op", "remove", "decision", dec, "outcome", "held")
	postCond(t, s, id, "path", "valid_in", "op", "remove", "decision", dec, "outcome", "held")
	if got := entityOf(t, s, id).ValidIn; !slices.Equal(got, []world.Condition{{Decision: dec, Outcome: "fell"}}) {
		t.Errorf("conditions = %v", got)
	}
	// "in every branch" is inside the chips, and comes back with the last removal.
	page := postCond(t, s, id, "path", "valid_in", "op", "remove", "decision", dec, "outcome", "fell")
	if !strings.Contains(page, "in every branch") {
		t.Errorf("no 'in every branch' after the last removal: %s", page)
	}
}

func TestVisibilityUnchangedAddsNothing(t *testing.T) {
	s, _ := newTestServer(t)
	postField(t, s, "char_miren", "visibility", "value", "public") // already public
	postField(t, s, "char_miren", "status", "value", "canon")
	if s.dirty("char_miren") {
		t.Error("setting a field to its own value made the entity dirty")
	}
}

// A date on an event that has none is created by its first edit.
func TestDateSetOnEventWithoutOne(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "evt_siege_of_vale"
	err := s.editDraft(id, func(d *Draft) error {
		d.Changes = append(d.Changes, world.Change{Path: []string{"date"}, Delete: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if entityOf(t, s, id).Date != nil {
		t.Fatal("the date is still there")
	}
	postField(t, s, id, "date", "sub", "era", "value", "third_reign")
	postField(t, s, id, "date", "sub", "earliest", "value", "412")
	iv := entityOf(t, s, id).Date
	if iv == nil || iv.Era != "third_reign" || iv.Earliest == nil || *iv.Earliest != 412 || iv.Latest != nil {
		t.Errorf("date = %+v", iv)
	}
}

// The last build's findings for the file show under their rows, by Path.
func TestBuildFindingsShownUnderRow(t *testing.T) {
	s, _ := newTestServer(t)
	const id = "char_miren"
	d, err := s.draft(id)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.findings = append(s.findings, world.Finding{
		Code: "bad_status", Severity: world.SeverityError, File: d.File, Path: "status", Msg: "status is not one of the four",
	}, world.Finding{
		Code: "other", Severity: world.SeverityError, File: "elsewhere.md", Path: "status", Msg: "belongs to another file",
	})
	s.mu.Unlock()
	page := do(t, s, "GET", "/entity/"+id+"/fields", nil).Body.String()
	if !strings.Contains(page, `class="lk-row lk-bad" data-field="status"`) || !strings.Contains(page, "status is not one of the four") {
		t.Errorf("the build's finding is not under its row:\n%s", page)
	}
	if strings.Contains(page, "belongs to another file") {
		t.Error("another file's finding is shown")
	}
}
