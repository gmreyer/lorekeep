package schema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// The files a pack is made of. Only packFile is required: the core version pin
// must never be absent by accident. A missing content file is an empty section.
const (
	packFile      = "pack.yaml"
	typesFile     = "entity-types.yaml"
	relationsFile = "relations.yaml"
	erasFile      = "eras.yaml"
	actsFile      = "acts.yaml"
)

// packMeta is pack.yaml: the pack's own identity and, for a project pack, the
// core version it was authored against.
//
// That pin is not the same fact as the lorekeep version in a world's go.mod.
// go.mod says which binary you run; core_version says which core vocabulary
// the prose was written against. They should agree, and the loader is what
// notices when someone bumps one without reviewing the other.
type packMeta struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	CoreVersion string `yaml:"core_version"`
	Description string `yaml:"description"`
}

type typesFileBody struct {
	Types  []EntityType `yaml:"types"`
	Groups []Group      `yaml:"groups"`
}

type relationsFileBody struct {
	Roles     []RoleDef  `yaml:"roles"`
	Relations []Relation `yaml:"relations"`
}

type erasFileBody struct {
	Eras []Era `yaml:"eras"`
}

type actsFileBody struct {
	Acts []Act `yaml:"acts"`
}

// LoadDir reads a project schema pack from a directory.
func LoadDir(dir string) (*Pack, error) {
	return loadFS(os.DirFS(dir), SourceProject)
}

// LoadFS reads a project schema pack rooted at the given filesystem.
func LoadFS(fsys fs.FS) (*Pack, error) {
	return loadFS(fsys, SourceProject)
}

// LoadProject reads a world's schema pack and merges it over the embedded core
// pack. This is the entry point everything else should use.
func LoadProject(dir string) (*Pack, error) {
	core, err := Core()
	if err != nil {
		return nil, err
	}
	project, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	return Merge(core, project)
}

func loadFS(fsys fs.FS, source Source) (*Pack, error) {
	l := &errList{source: source}

	meta, ok := readPackMeta(fsys, l, source)
	if !ok {
		return nil, l.err()
	}

	p := &Pack{
		Name:        meta.Name,
		Version:     meta.Version,
		CoreVersion: meta.CoreVersion,
		Description: meta.Description,
		Source:      source,
	}

	var types typesFileBody
	if readOptional(fsys, l, typesFile, &types) {
		p.Types, p.Groups = types.Types, types.Groups
	}
	var rels relationsFileBody
	if readOptional(fsys, l, relationsFile, &rels) {
		p.Roles, p.Relations = rels.Roles, rels.Relations
	}
	var eras erasFileBody
	if readOptional(fsys, l, erasFile, &eras) {
		p.Eras = eras.Eras
	}
	var acts actsFileBody
	if readOptional(fsys, l, actsFile, &acts) {
		p.Acts = acts.Acts
	}

	checkTypes(l, p)
	checkRelations(l, p)
	checkRoles(l, p)
	checkEras(l, p)
	checkActs(l, p)

	if err := l.err(); err != nil {
		return nil, err
	}
	p.index()
	return p, nil
}

func readPackMeta(fsys fs.FS, l *errList, source Source) (packMeta, bool) {
	data, err := fs.ReadFile(fsys, packFile)
	if err != nil {
		l.add(CodeNotAPack, packFile, "",
			"no %s here, so this is not a schema pack", packFile)
		return packMeta{}, false
	}

	var meta packMeta
	if err := decodeStrict(data, &meta); err != nil {
		l.add(codeForYAML(err), packFile, "", "%s", yamlMessage(err))
		return packMeta{}, false
	}

	if meta.Version == "" {
		l.add(CodeMissingField, packFile, "version", "a pack must declare its own version")
	} else if _, err := parseVersion(meta.Version); err != nil {
		l.add(CodeBadVersion, packFile, "version", "%s", err)
	}

	switch source {
	case SourceProject:
		if meta.CoreVersion == "" {
			l.add(CodeMissingField, packFile, "core_version",
				"a project pack must pin the core version it was authored against")
		} else if _, err := parseVersion(meta.CoreVersion); err != nil {
			l.add(CodeBadVersion, packFile, "core_version", "%s", err)
		}
	case SourceCore:
		if meta.CoreVersion != "" {
			l.add(CodeBadVersion, packFile, "core_version",
				"the core pack cannot pin a core version; it is one")
		}
	}

	// Metadata faults do not stop the content files being read: reporting
	// every fault in one pass is the point.
	return meta, true
}

// readOptional decodes a content file if it is present. A missing file is an
// empty section, not an error.
func readOptional(fsys fs.FS, l *errList, name string, out any) bool {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		l.add(CodeParse, name, "", "cannot read: %v", err)
		return false
	}
	if err := decodeStrict(data, out); err != nil {
		l.add(codeForYAML(err), name, "", "%s", yamlMessage(err))
		return false
	}
	return true
}

// decodeStrict rejects unknown fields, so a typo in a pack fails the build
// rather than being silently dropped.
func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // an empty file is an empty section
		}
		return err
	}
	return nil
}

func codeForYAML(err error) Code {
	var te *yaml.TypeError
	if errors.As(err, &te) && strings.Contains(err.Error(), "not found in type") {
		return CodeUnknownField
	}
	return CodeParse
}

func yamlMessage(err error) string {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		return strings.Join(te.Errors, "; ")
	}
	return err.Error()
}

// reservedTypeNames are names no pack may declare as an entity type or a
// group.
//
// "statement" is reserved because a world file routes to the statement loader
// on `type: statement`, and because a Statement may never be a relation
// endpoint. Domain and range admit only declared entity types, so keeping the
// name out of Pack.Types makes that structural rather than a rule enforced
// somewhere else. The authoring side of this pairing is world.StatementType.
var reservedTypeNames = map[string]string{
	"statement": "the kind a world file declares with `type: statement`; a statement is not an entity type and may never be a relation endpoint",
}

// checkReserved reports a name that a pack may not claim. Folding the case
// matters for the same reason it does everywhere else here: Windows would
// treat Statement and statement as one name.
func checkReserved(l *errList, file, path, name, kind string) bool {
	why, ok := reservedTypeNames[strings.ToLower(name)]
	if !ok {
		return false
	}
	l.add(CodeReservedName, file, path,
		"%s %q uses a reserved name: %s", kind, name, why)
	return true
}

func checkTypes(l *errList, p *Pack) {
	// Entity types and groups share one namespace: both are usable wherever a
	// type name is, so a group named after a type would be ambiguous.
	ns := newNamespace()
	for i, t := range p.Types {
		path := fmt.Sprintf("types[%d].name", i)
		if t.Name == "" {
			l.add(CodeMissingField, typesFile, path, "an entity type must have a name")
			continue
		}
		if checkReserved(l, typesFile, path, t.Name, "entity type") {
			continue
		}
		ns.claim(l, typesFile, path, t.Name, "entity type")
	}
	for i, g := range p.Groups {
		path := fmt.Sprintf("groups[%d].name", i)
		if g.Name == "" {
			l.add(CodeMissingField, typesFile, path, "a group must have a name")
			continue
		}
		if checkReserved(l, typesFile, path, g.Name, "group") {
			continue
		}
		if len(g.Includes) == 0 {
			l.add(CodeMissingField, typesFile, fmt.Sprintf("groups[%d].includes", i),
				"group %q includes nothing", g.Name)
		}
		ns.claim(l, typesFile, path, g.Name, "group")
	}
}

func checkRelations(l *errList, p *Pack) {
	// Relation names and derived inverse names share one namespace. An inverse
	// is a name the index will mint, so nothing else may hold it.
	ns := newNamespace()

	for i, r := range p.Relations {
		base := fmt.Sprintf("relations[%d]", i)
		if r.Name == "" {
			l.add(CodeMissingField, relationsFile, base+".name", "a relation must have a name")
			continue
		}

		var missing []string
		if len(r.Domain) == 0 {
			missing = append(missing, "domain")
		}
		if len(r.Range) == 0 {
			missing = append(missing, "range")
		}
		if len(missing) > 0 {
			l.add(CodeMissingField, relationsFile, base,
				"relation %q declares no %s", r.Name, strings.Join(missing, " and no "))
		}

		switch {
		case r.Symmetric && r.Inverse != "":
			l.add(CodeSymmetricInverse, relationsFile, base+".inverse",
				"relation %q is symmetric, so it is its own inverse; remove inverse %q",
				r.Name, r.Inverse)
		case r.Inverse == r.Name && r.Inverse != "":
			l.add(CodeSelfInverse, relationsFile, base+".inverse",
				"relation %q names itself as its inverse; declare symmetric: true instead",
				r.Name)
		}
		if r.Symmetric && !sameTypeSet(r.Domain, r.Range) {
			l.add(CodeSymmetricDomain, relationsFile, base,
				"relation %q is symmetric, so its domain and range must match (%v vs %v)",
				r.Name, r.Domain, r.Range)
		}

		ns.claim(l, relationsFile, base+".name", r.Name, "relation")
		if r.Inverse != "" && r.Inverse != r.Name {
			ns.claim(l, relationsFile, base+".inverse", r.Inverse, "inverse")
		}
	}
}

func checkRoles(l *errList, p *Pack) {
	ns := newNamespace()
	for i, r := range p.Roles {
		path := fmt.Sprintf("roles[%d].name", i)
		if r.Name == "" {
			l.add(CodeMissingField, relationsFile, path, "a role must have a name")
			continue
		}
		ns.claim(l, relationsFile, path, string(r.Name), "role")
	}
}

func checkEras(l *errList, p *Pack) {
	ns := newNamespace()
	for i, e := range p.Eras {
		path := fmt.Sprintf("eras[%d].key", i)
		if e.Key == "" {
			l.add(CodeMissingField, erasFile, path, "an era must have a key")
			continue
		}
		ns.claim(l, erasFile, path, e.Key, "era")
	}
}

func checkActs(l *errList, p *Pack) {
	ns := newNamespace()
	for i, a := range p.Acts {
		path := fmt.Sprintf("acts[%d].key", i)
		if a.Key == "" {
			l.add(CodeMissingField, actsFile, path, "an act must have a key")
			continue
		}
		ns.claim(l, actsFile, path, a.Key, "act")
	}
}

// namespace enforces that a set of names is unique both exactly and
// case-insensitively. The second check is not fussiness: Windows treats two
// filenames differing only in case as one file and git does not, so a pair
// that works for one writer collides for another.
type namespace struct {
	exact map[string]string // name -> what claimed it
	fold  map[string]string // lowercased name -> the name that claimed it
}

func newNamespace() *namespace {
	return &namespace{exact: map[string]string{}, fold: map[string]string{}}
}

func (n *namespace) claim(l *errList, file, path, name, kind string) {
	if prev, ok := n.exact[name]; ok {
		l.add(CodeDuplicateName, file, path,
			"%s %q is already declared as a %s in this pack", kind, name, prev)
		return
	}
	lower := strings.ToLower(name)
	if prev, ok := n.fold[lower]; ok {
		l.add(CodeCaseCollision, file, path,
			"%s %q collides with %q when case is ignored; Windows treats them as one name",
			kind, name, prev)
		return
	}
	n.exact[name] = kind
	n.fold[lower] = name
}

func sameTypeSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
