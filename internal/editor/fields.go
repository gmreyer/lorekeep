package editor

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

// The inline fields form (round 4) and the "only when" chips every valid_in
// is edited with. Every edit appends world.Change values to the entity's
// draft and renders the form again from the draft; nothing reaches disk until
// the writer saves.

func (s *Server) fieldRoutes() {
	s.mux.HandleFunc("GET /entity/{id}/fields", s.fieldsOpen)
	s.mux.HandleFunc("GET /entity/{id}/fields/line", s.fieldsFold)
	s.mux.HandleFunc("POST /entity/{id}/fields/{field}", s.fieldEdit)
	s.mux.HandleFunc("GET /entity/{id}/conditions", s.condChips)
	s.mux.HandleFunc("GET /entity/{id}/conditions/pick", s.condPick)
	s.mux.HandleFunc("POST /entity/{id}/conditions", s.condEdit)
}

// The form's rows are the authored file format's fields, in file order.
const (
	fieldName       = "name"
	fieldAliases    = "aliases"
	fieldStatus     = "status"
	fieldVisibility = "visibility"
	fieldLifespan   = "lifespan"
	fieldDate       = "date"
	fieldOutcomes   = "outcomes"
	fieldValidIn    = "valid_in"
)

// fieldOrder is the form's rows, in file order.
var fieldOrder = []string{
	fieldName, fieldAliases, fieldStatus, fieldVisibility,
	fieldLifespan, fieldDate, fieldOutcomes, fieldValidIn,
}

// typedFields are the fields that belong to one entity type only. It mirrors
// the table in internal/validate/entities.go (checkFieldPlacement): a field
// listed here is a row only on its owner type, and is shown read-only, as an
// unknown field, anywhere else.
var typedFields = map[string]string{
	fieldLifespan: schema.TypeCharacter,
	fieldDate:     schema.TypeEvent,
	fieldOutcomes: schema.TypeDecision,
}

// handledKeys are the frontmatter keys the editor knows: the rows above, and
// the fields other panels own (relations, beliefs, asserts) or that identify
// the file (id, type). Any other key is shown read-only and kept.
var handledKeys = map[string]bool{
	"id": true, "type": true, "relations": true, "beliefs": true, "asserts": true,
}

func fieldApplies(field, typ string) bool {
	if !slices.Contains(fieldOrder, field) {
		return false
	}
	owner, typed := typedFields[field]
	return !typed || owner == typ
}

// ---- view model ----

type rowKind int

const (
	rowText rowKind = iota
	rowList
	rowInterval
	rowChoice
	rowConds
	rowReadOnly
)

type choice struct {
	Value, Label string
	On           bool
}

type option struct {
	Value, Label string
	Selected     bool
}

type intervalRow struct {
	Eras      []option
	EraSet    bool
	Earliest  string
	Latest    string
	Precision []choice
}

type fieldRow struct {
	Field, Label string
	Kind         rowKind
	Text         string
	Items        []string
	AddLabel     string
	Interval     intervalRow
	Choices      []choice
	// Acts is the act dropdown of a spoiler visibility.
	Acts   []option
	Conds  CondTarget
	Raw    string
	Errors []string
}

type linePart struct{ Label, Value string }

// fieldsData is everything the fields form and its folded line show.
type fieldsData struct {
	ID string
	// Name is the entity's name as the draft has it.
	Name string
	Line []linePart
	Rows []fieldRow
	// OOBTitle makes the response replace the page title, after a rename.
	OOBTitle bool
}

// Closed sets of the file format, as the labels the form shows.
var (
	statuses = []world.Status{world.StatusDraft, world.StatusCanon, world.StatusDeprecated, world.StatusNonCanon}
	// visibilities are the kinds; a spoiler also names an act.
	visibilities = []world.VisibilityKind{world.VisibilityPublic, world.VisibilitySpoiler, world.VisibilityInternal}
)

// fieldsData reads the entity's draft (or its file) and what the form needs
// of the project: eras, acts and decision names.
func (s *Server) fieldsData(id string) (fieldsData, error) {
	d, err := s.draft(id)
	if err != nil {
		return fieldsData{}, err
	}
	ent, parseFs, err := d.Entity()
	if err != nil {
		return fieldsData{}, err
	}
	raw, err := d.Bytes()
	if err != nil {
		return fieldsData{}, err
	}
	var eras, acts []option
	var valid CondTarget
	err = s.read(func(cur *loaded) error {
		for _, e := range cur.idx.Vocabulary.Eras {
			eras = append(eras, option{Value: e.Key, Label: e.Name})
		}
		for _, a := range cur.idx.Vocabulary.Acts {
			acts = append(acts, option{Value: a.Key, Label: a.Name})
		}
		valid = s.condTargetIn(cur, id, []string{fieldValidIn}, ent.ValidIn)
		return nil
	})
	if err != nil {
		return fieldsData{}, err
	}
	// After s.read, which may have rebuilt: the findings are the build's
	// that the form shows.
	fs := append(slices.Clone(parseFs), s.fileFindings(d.File)...)
	return buildFields(id, ent, raw, fs, eras, acts, valid), nil
}

func buildFields(id string, ent *world.Entity, raw []byte, fs []world.Finding, eras, acts []option, valid CondTarget) fieldsData {
	fd := fieldsData{ID: id, Name: ent.Name}
	errs := func(key string) []string { return rowErrors(fs, key) }

	for _, f := range fieldOrder {
		if !fieldApplies(f, ent.Type) {
			continue
		}
		r := fieldRow{Field: f, Label: f, Errors: errs(f)}
		switch f {
		case fieldName:
			r.Kind, r.Text = rowText, ent.Name
		case fieldAliases:
			r.Kind, r.Items, r.AddLabel = rowList, ent.Aliases, "+ alias"
		case fieldOutcomes:
			r.Kind, r.Items, r.AddLabel = rowList, ent.Outcomes, "+ outcome"
		case fieldStatus:
			r.Kind = rowChoice
			for _, st := range statuses {
				r.Choices = append(r.Choices, choice{Value: string(st), Label: string(st), On: ent.Status == st})
			}
		case fieldVisibility:
			r.Kind = rowChoice
			for _, k := range visibilities {
				r.Choices = append(r.Choices, choice{Value: string(k), Label: string(k), On: ent.Visibility.Kind == k})
			}
			if ent.Visibility.Kind == world.VisibilitySpoiler {
				r.Acts = slices.Clone(acts)
				found := false
				for i := range r.Acts {
					if r.Acts[i].Value == ent.Visibility.Act {
						r.Acts[i].Selected, found = true, true
					}
				}
				if !found {
					r.Acts = append([]option{{Value: ent.Visibility.Act, Label: ent.Visibility.Act, Selected: true}}, r.Acts...)
				}
			}
		case fieldLifespan:
			r.Kind, r.Interval = rowInterval, intervalRowOf(ent.Lifespan, eras)
		case fieldDate:
			r.Kind, r.Interval = rowInterval, intervalRowOf(ent.Date, eras)
		case fieldValidIn:
			r.Label, r.Kind, r.Conds = "exists", rowConds, valid
		}
		fd.Rows = append(fd.Rows, r)
	}
	for _, x := range extraFields(raw, ent.Type) {
		x.Errors = errs(x.Field)
		for _, f := range fs {
			// The parser locates an unknown field by line, not by Path, so a
			// finding without a Path is matched on the field it names.
			if f.Code == world.CodeUnknownField && f.Path == "" && strings.Contains(f.Msg, "field "+x.Field+" ") {
				x.Errors = appendUnique(x.Errors, f.Msg)
			}
		}
		fd.Rows = append(fd.Rows, x)
	}

	// The folded line: the fields that are set, quietly.
	if len(ent.Aliases) > 0 {
		fd.Line = append(fd.Line, linePart{"aliases", strings.Join(ent.Aliases, ", ")})
	}
	if t := intervalText(ent.Lifespan); t != "" {
		fd.Line = append(fd.Line, linePart{"lifespan", t})
	}
	if t := intervalText(ent.Date); t != "" {
		fd.Line = append(fd.Line, linePart{"date", t})
	}
	if len(ent.Outcomes) > 0 {
		fd.Line = append(fd.Line, linePart{"outcomes", strings.Join(ent.Outcomes, ", ")})
	}
	if ent.Visibility.Kind != world.VisibilityPublic && ent.Visibility.Raw != "" {
		fd.Line = append(fd.Line, linePart{"visibility", ent.Visibility.Raw})
	}
	if len(ent.ValidIn) > 0 {
		var cs []string
		for _, c := range ent.ValidIn {
			cs = append(cs, valid.name(c.Decision)+" = "+c.Outcome)
		}
		fd.Line = append(fd.Line, linePart{"only when", strings.Join(cs, " and ")})
	}
	return fd
}

func appendUnique(xs []string, x string) []string {
	if slices.Contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

func intervalRowOf(iv *world.Interval, eras []option) intervalRow {
	var cur world.Interval
	if iv != nil {
		cur = *iv
	}
	r := intervalRow{Eras: slices.Clone(eras), EraSet: cur.Era != ""}
	found := cur.Era == ""
	for i := range r.Eras {
		if r.Eras[i].Value == cur.Era {
			r.Eras[i].Selected, found = true, true
		}
	}
	if !found {
		r.Eras = append(r.Eras, option{Value: cur.Era, Label: cur.Era, Selected: true})
	}
	if cur.Earliest != nil {
		r.Earliest = strconv.Itoa(*cur.Earliest)
	}
	if cur.Latest != nil {
		r.Latest = strconv.Itoa(*cur.Latest)
	}
	for _, p := range []world.Precision{world.PrecisionExact, world.PrecisionApproximate} {
		r.Precision = append(r.Precision, choice{Value: string(p), Label: string(p), On: cur.Precision == p})
	}
	return r
}

// intervalText is an interval as one short phrase: "third_reign 380–419".
func intervalText(iv *world.Interval) string {
	if iv == nil || iv.Empty() {
		return ""
	}
	var parts []string
	if iv.Era != "" {
		parts = append(parts, iv.Era)
	}
	if iv.Earliest != nil || iv.Latest != nil {
		span := ""
		if iv.Earliest != nil {
			span = strconv.Itoa(*iv.Earliest)
		}
		if iv.Latest != nil {
			span += "–" + strconv.Itoa(*iv.Latest)
		} else if span != "" {
			span += "–"
		}
		parts = append(parts, span)
	}
	if iv.Precision == world.PrecisionApproximate {
		parts = append(parts, "(approx.)")
	}
	return strings.Join(parts, " ")
}

// rowErrors are the findings that name the field key, as their messages.
func rowErrors(fs []world.Finding, key string) []string {
	var out []string
	for _, f := range fs {
		if f.Path == key || strings.HasPrefix(f.Path, key+".") || strings.HasPrefix(f.Path, key+"[") {
			out = appendUnique(out, f.Msg)
		}
	}
	return out
}

// fileFindings are the last build's findings for one file (world-relative).
func (s *Server) fileFindings(file string) []world.Finding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []world.Finding
	for _, f := range s.findings {
		if f.File == file {
			out = append(out, f)
		}
	}
	return out
}

// extraFields are the frontmatter keys the form has no row for, shown
// read-only: a key it does not know, or a typed field on the wrong type.
// EditFrontmatter keeps them untouched.
func extraFields(data []byte, typ string) []fieldRow {
	block, ok := frontmatterBlock(data)
	if !ok {
		return nil
	}
	var doc yaml.Node
	if yaml.Unmarshal([]byte(block), &doc) != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 ||
		doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	m := doc.Content[0]
	var out []fieldRow
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i].Value, m.Content[i+1]
		if handledKeys[key] || (slices.Contains(fieldOrder, key) && fieldApplies(key, typ)) {
			continue
		}
		out = append(out, fieldRow{Field: key, Label: key, Kind: rowReadOnly, Raw: flowText(val)})
	}
	return out
}

func flowText(n *yaml.Node) string {
	c := *n
	if c.Kind == yaml.MappingNode || c.Kind == yaml.SequenceNode {
		c.Style |= yaml.FlowStyle
	}
	out, err := yaml.Marshal(&c)
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// frontmatterBlock is the YAML between the fences, found by the parser's
// rules: optional BOM and blank lines, then a --- line to the next one.
func frontmatterBlock(data []byte) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})), "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.TrimRight(l, " \t") == "---" {
			start = i
		}
		break
	}
	if start < 0 {
		return "", false
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return strings.Join(lines[start+1:i], "\n"), true
		}
	}
	return "", false
}

// ---- handlers ----

func (s *Server) fieldsOpen(w http.ResponseWriter, r *http.Request) {
	fd, err := s.fieldsData(r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, fieldsForm(fd))
}

func (s *Server) fieldsFold(w http.ResponseWriter, r *http.Request) {
	fd, err := s.fieldsData(r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, fieldsLine(fd))
}

// fieldEdit appends one edit of one field to the entity's draft, then shows
// the form as the draft leaves it.
func (s *Server) fieldEdit(w http.ResponseWriter, r *http.Request) {
	id, field := r.PathValue("id"), r.PathValue("field")
	if err := r.ParseForm(); err != nil {
		badRequest(w, "%v", err)
		return
	}
	// The edit is worked out against the draft as it is now, inside
	// editDraft, so an item removed by value is found wherever it has moved.
	err := s.editDraft(id, func(d *Draft) error {
		ent, _, err := d.Entity()
		if err != nil {
			return err
		}
		if !fieldApplies(field, ent.Type) || field == fieldValidIn {
			return refuse("no field %q to edit on this entity", field)
		}
		var changes []world.Change
		err = s.read(func(cur *loaded) error {
			var err error
			changes, err = fieldChanges(cur, field, r.PostForm, ent)
			return err
		})
		if err != nil {
			return err
		}
		d.Changes = append(d.Changes, changes...)
		return nil
	})
	var ue userError
	switch {
	case errors.As(err, &ue):
		badRequest(w, "%v", ue)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	fd, err := s.fieldsData(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	fd.OOBTitle = field == fieldName
	trigger(w, eventDirty)
	render(w, r, fieldsForm(fd))
}

// userError is a post the editor refuses: a 400, not a fault.
type userError struct{ error }

func refuse(format string, args ...any) error { return userError{fmt.Errorf(format, args...)} }

// fieldChanges turns one form post into the Changes it means, or nothing when
// the post changes nothing.
func fieldChanges(cur *loaded, field string, f url.Values, ent *world.Entity) ([]world.Change, error) {
	value := strings.TrimSpace(f.Get("value"))
	set := func(v any, path ...string) []world.Change { return []world.Change{{Path: path, Value: v}} }

	switch field {
	case fieldName:
		if value == ent.Name {
			return nil, nil
		}
		return set(value, fieldName), nil

	case fieldAliases, fieldOutcomes:
		items := ent.Aliases
		if field == fieldOutcomes {
			items = ent.Outcomes
		}
		switch f.Get("op") {
		case "add":
			if value == "" || slices.Contains(items, value) {
				return nil, nil
			}
			return set(value, field, "-"), nil
		case "remove":
			// By value, against the draft as it is now: a second click on a
			// stale ×, or a click after another removal, cannot hit another item.
			i := slices.Index(items, value)
			if i < 0 {
				return nil, nil // already gone; the form shows what is left
			}
			if len(items) == 1 {
				return []world.Change{{Path: []string{field}, Delete: true}}, nil
			}
			return []world.Change{{Path: []string{field, strconv.Itoa(i)}, Delete: true}}, nil
		}
		return nil, refuse("op must be add or remove")

	case fieldStatus:
		st := world.Status(value)
		if !st.Valid() {
			return nil, refuse("%q is not a status", value)
		}
		if st == ent.Status {
			return nil, nil
		}
		return set(value, fieldStatus), nil

	case fieldVisibility:
		return visibilityChange(cur, f, value, ent)

	case fieldLifespan, fieldDate:
		iv := ent.Lifespan
		if field == fieldDate {
			iv = ent.Date
		}
		return intervalChange(cur, field, f.Get("sub"), value, iv)
	}
	return nil, refuse("no field %q to edit", field)
}

func visibilityChange(cur *loaded, f url.Values, value string, ent *world.Entity) ([]world.Change, error) {
	acts := cur.idx.Vocabulary.Acts
	var want string
	switch {
	case f.Get("sub") == "act":
		if !slices.ContainsFunc(acts, func(a index.Ordered) bool { return a.Key == value }) {
			return nil, refuse("%q is not an act of this project", value)
		}
		want = string(world.VisibilitySpoiler) + ":" + value
	case value == string(world.VisibilitySpoiler):
		act := ent.Visibility.Act
		if act == "" {
			if len(acts) == 0 {
				return nil, refuse("this project declares no acts, so nothing can be a spoiler")
			}
			act = acts[0].Key
		}
		want = string(world.VisibilitySpoiler) + ":" + act
	case value == string(world.VisibilityPublic), value == string(world.VisibilityInternal):
		want = value
	default:
		return nil, refuse("%q is not a visibility", value)
	}
	// Compare with the effective value: a file with no visibility key reads
	// as public, and clicking the lit "public" adds nothing.
	if want == ent.Visibility.Raw || want == ent.Visibility.String() {
		return nil, nil
	}
	return []world.Change{{Path: []string{fieldVisibility}, Value: want}}, nil
}

func intervalChange(cur *loaded, field, sub, value string, iv *world.Interval) ([]world.Change, error) {
	var next world.Interval
	if iv != nil {
		next = *iv
	}
	before := next
	switch sub {
	case "era":
		if value != "" && !slices.ContainsFunc(cur.idx.Vocabulary.Eras, func(e index.Ordered) bool { return e.Key == value }) {
			return nil, refuse("%q is not an era of this project", value)
		}
		next.Era = value
	case "earliest", "latest":
		var p *int
		if value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, refuse("%q is not a whole number", value)
			}
			p = &n
		}
		if sub == "earliest" {
			next.Earliest = p
		} else {
			next.Latest = p
		}
	case "precision":
		p := world.Precision(value)
		if (p != world.PrecisionExact && p != world.PrecisionApproximate) || p == next.Precision {
			p = "" // the active segment, clicked again, clears it
		}
		next.Precision = p
	default:
		return nil, refuse("sub must be era, earliest, latest or precision")
	}
	if sameInterval(next, before) {
		return nil, nil
	}
	if next.Empty() && next.Precision == "" {
		return []world.Change{{Path: []string{field}, Delete: true}}, nil
	}
	var v any
	switch sub {
	case "era":
		v = next.Era
	case "earliest":
		if next.Earliest != nil {
			v = *next.Earliest
		}
	case "latest":
		if next.Latest != nil {
			v = *next.Latest
		}
	case "precision":
		v = string(next.Precision)
	}
	c := world.Change{Path: []string{field, sub}, Value: v}
	if v == nil || v == "" {
		c = world.Change{Path: []string{field, sub}, Delete: true}
	}
	return []world.Change{c}, nil
}

func sameInterval(a, b world.Interval) bool {
	eq := func(x, y *int) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Era == b.Era && a.Precision == b.Precision && eq(a.Earliest, b.Earliest) && eq(a.Latest, b.Latest)
}

// ---- "only when" chips ----

// CondTarget is one valid_in list to edit with "only when" chips: the
// entity's own, a relation's, or a belief's (round 4).
type CondTarget struct {
	// Entity is the entity whose draft holds the list.
	Entity string
	// Path is the list's world.Change path in that file: {"valid_in"},
	// {"relations", "2", "valid_in"}, {"beliefs", "0", "valid_in"}.
	Path []string
	// Conds are the conditions as the draft has them.
	Conds []world.Condition
	// Names are the decisions' names by ID, from s.condTarget. Without them
	// the chips load themselves, so a caller that has no resolver at hand
	// still gets names on the screen.
	Names map[string]string
}

// condTarget is a CondTarget with its decisions' names filled in. The caller
// is outside s.read.
func (s *Server) condTarget(entity string, path []string, conds []world.Condition) (CondTarget, error) {
	t := CondTarget{Entity: entity, Path: path, Conds: conds}
	err := s.read(func(cur *loaded) error {
		t = s.condTargetIn(cur, entity, path, conds)
		return nil
	})
	return t, err
}

// condTargetIn is condTarget under an open s.read.
func (s *Server) condTargetIn(cur *loaded, entity string, path []string, conds []world.Condition) CondTarget {
	t := CondTarget{Entity: entity, Path: path, Conds: conds, Names: map[string]string{}}
	for _, c := range conds {
		if e, err := cur.res.Entity(s.readContext(), c.Decision); err == nil {
			t.Names[c.Decision] = e.Name
		}
	}
	return t
}

// name is a decision's name for a chip: never its ID.
func (t CondTarget) name(decision string) string {
	if n, ok := t.Names[decision]; ok {
		return n
	}
	return "a missing decision"
}

func (t CondTarget) needsNames() bool { return len(t.Conds) > 0 && t.Names == nil }

// wrapID is the chips' wrapper: derived from the entity and the path, so each
// list re-renders itself.
func (t CondTarget) wrapID() string {
	return "lk-cond-" + t.Entity + "-" + strings.Join(t.Path, "-")
}

func (t CondTarget) query() string { return "path=" + url.QueryEscape(strings.Join(t.Path, ".")) }

func (t CondTarget) chipsURL() string { return "/entity/" + t.Entity + "/conditions?" + t.query() }
func (t CondTarget) pickURL() string  { return "/entity/" + t.Entity + "/conditions/pick?" + t.query() }
func (t CondTarget) postURL() string  { return "/entity/" + t.Entity + "/conditions" }

var condIndex = regexp.MustCompile(`^[0-9]+$`)

// parseCondPath validates the path of a valid_in list: the root's, or an
// item's of relations, beliefs or asserts. Nothing else is writable here.
func parseCondPath(p string) ([]string, error) {
	segs := strings.Split(p, ".")
	switch {
	case len(segs) == 1 && segs[0] == fieldValidIn:
		return segs, nil
	case len(segs) == 3 && segs[2] == fieldValidIn && condIndex.MatchString(segs[1]) &&
		(segs[0] == "relations" || segs[0] == "beliefs" || segs[0] == "asserts"):
		return segs, nil
	}
	return nil, refuse("%q is not a valid_in list", p)
}

// condsAt is the conditions at path in the entity, and whether the path
// exists in it.
func condsAt(ent *world.Entity, path []string) ([]world.Condition, bool) {
	if len(path) == 1 {
		return ent.ValidIn, true
	}
	i, err := strconv.Atoi(path[1])
	if err != nil {
		return nil, false
	}
	switch path[0] {
	case "relations":
		if i < len(ent.Relations) {
			return ent.Relations[i].ValidIn, true
		}
	case "beliefs":
		if i < len(ent.Beliefs) {
			return ent.Beliefs[i].ValidIn, true
		}
	case "asserts":
		if i < len(ent.Asserts) {
			return ent.Asserts[i].ValidIn, true
		}
	}
	return nil, false
}

// condTargetOf reads the draft's list at the request's path.
func (s *Server) condTargetOf(r *http.Request, get func(string) string) (CondTarget, int, error) {
	id := r.PathValue("id")
	path, err := parseCondPath(get("path"))
	if err != nil {
		return CondTarget{}, http.StatusBadRequest, err
	}
	d, err := s.draft(id)
	if err != nil {
		return CondTarget{}, http.StatusNotFound, err
	}
	ent, _, err := d.Entity()
	if err != nil {
		return CondTarget{}, http.StatusInternalServerError, err
	}
	conds, ok := condsAt(ent, path)
	if !ok {
		return CondTarget{}, http.StatusBadRequest, fmt.Errorf("%q is not in this entity", get("path"))
	}
	t, err := s.condTarget(id, path, conds)
	if err != nil {
		return CondTarget{}, http.StatusInternalServerError, err
	}
	return t, 0, nil
}

func (s *Server) condChips(w http.ResponseWriter, r *http.Request) {
	t, code, err := s.condTargetOf(r, r.URL.Query().Get)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	render(w, r, conditionChips(t))
}

// decisionOption is a decision a condition can name, with its outcomes.
type decisionOption struct {
	ID, Name string
	Outcomes []string
}

// decisions are the decisions present under the editor's context.
func (s *Server) decisions(cur *loaded) []decisionOption {
	ctx := s.readContext()
	ents, err := cur.res.Entities(ctx)
	if err != nil {
		return nil
	}
	var out []decisionOption
	for _, sm := range ents {
		if sm.Type != schema.TypeDecision {
			continue
		}
		e, err := cur.res.Entity(ctx, sm.ID)
		if err != nil {
			continue
		}
		out = append(out, decisionOption{ID: e.ID, Name: e.Name, Outcomes: e.Outcomes})
	}
	slices.SortFunc(out, func(a, b decisionOption) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

func (s *Server) condPick(w http.ResponseWriter, r *http.Request) {
	t, code, err := s.condTargetOf(r, r.URL.Query().Get)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	var decs []decisionOption
	err = s.read(func(cur *loaded) error {
		decs = s.decisions(cur)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	render(w, r, condPicker(t, decs))
}

// condEdit adds or removes one condition of one list, then shows the list's
// chips. Several conditions mean all must hold.
func (s *Server) condEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		badRequest(w, "%v", err)
		return
	}
	t, code, err := s.condTargetOf(r, r.PostForm.Get)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	want := world.Condition{Decision: r.PostForm.Get("decision"), Outcome: r.PostForm.Get("outcome")}
	op := r.PostForm.Get("op")
	switch op {
	case "add":
		var ok bool
		err = s.read(func(cur *loaded) error {
			for _, dec := range s.decisions(cur) {
				if dec.ID == want.Decision && slices.Contains(dec.Outcomes, want.Outcome) {
					ok = true
				}
			}
			return nil
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !ok {
			badRequest(w, "that decision and outcome do not exist")
			return
		}
	case "remove":
	default:
		badRequest(w, "op must be add or remove")
		return
	}
	// The change is worked out against the draft's list as it is now, so a
	// condition removed by value cannot hit another one after a stale click.
	err = s.editDraft(id, func(d *Draft) error {
		ent, _, err := d.Entity()
		if err != nil {
			return err
		}
		conds, _ := condsAt(ent, t.Path)
		i := slices.Index(conds, want)
		switch {
		case op == "add" && i < 0:
			d.Changes = append(d.Changes, world.Change{Path: append(slices.Clone(t.Path), "-"), Value: want})
		case op == "remove" && i >= 0 && len(conds) == 1:
			d.Changes = append(d.Changes, world.Change{Path: slices.Clone(t.Path), Delete: true})
		case op == "remove" && i >= 0:
			d.Changes = append(d.Changes, world.Change{Path: append(slices.Clone(t.Path), strconv.Itoa(i)), Delete: true})
		default:
			return nil
		}
		trigger(w, eventDirty)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, code, err = s.condTargetOf(r, r.PostForm.Get)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	render(w, r, conditionChips(t))
}

// ---- helpers for templates ----

func fieldURL(id, field string) string { return "/entity/" + id + "/fields/" + field }

// vals is an hx-vals value from key, value pairs.
func vals(kv ...string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	out, _ := json.Marshal(m) // a map of strings always encodes
	return string(out)
}

// condVals is the hx-vals of a post to a CondTarget's list.
func (t CondTarget) vals(kv ...string) string {
	return vals(append([]string{"path", strings.Join(t.Path, ".")}, kv...)...)
}
