package schema

import (
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestTypePrefixDefaultsToName pins where a created ID's prefix comes from: the
// type's own prefix when the pack declares one, otherwise the type name, so a
// project type the editor has never heard of still gets IDs.
func TestTypePrefixDefaultsToName(t *testing.T) {
	project, err := LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: ship
  - name: guild_house
    prefix: gh
groups:
  - name: vessel
    includes: [ship]
`}))
	if err != nil {
		t.Fatal(err)
	}
	core, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Merge(core, project)
	if err != nil {
		t.Fatal(err)
	}

	for typ, want := range map[string]string{"ship": "ship", "guild_house": "gh"} {
		got, ok := p.IDPrefix(typ)
		if !ok || got != want {
			t.Errorf("IDPrefix(%q) = %q, %v; want %q, true", typ, got, ok, want)
		}
	}
	// A group is not a type, and nothing is created as one.
	for _, name := range []string{"vessel", "statement", "nothing"} {
		if got, ok := p.IDPrefix(name); ok {
			t.Errorf("IDPrefix(%q) = %q, true; want false", name, got)
		}
	}
}

// TestCorePackPrefixes pins the core types' prefixes. Created IDs carry them
// for good, since nothing renames an ID, so changing one is a deliberate core
// change recorded here.
func TestCorePackPrefixes(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"character": "char",
		"faction":   "fac",
		"location":  "loc",
		"event":     "evt",
		"object":    "obj",
		"concept":   "cpt",
		"decision":  "dec",
	}
	got := map[string]string{}
	for _, typ := range c.Types {
		got[typ.Name] = typ.Prefix
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("core prefixes (-want +got):\n%s", diff)
	}
	for typ := range maps.Keys(want) {
		if p, _ := c.IDPrefix(typ); p != want[typ] {
			t.Errorf("IDPrefix(%q) = %q, want %q", typ, p, want[typ])
		}
	}
}

// TestPrefixMustBeIDSafe keeps a created ID valid: prefix, underscore, six
// characters must match the validator's ID rule, and a doubled or trailing
// underscore would make IDs that read as a typo.
func TestPrefixMustBeIDSafe(t *testing.T) {
	for _, bad := range []string{"Fac", "fac-x", "fa c", "_fac", "fac_", "fac__x", "fäc"} {
		_, err := LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: guild
    prefix: "` + bad + `"
`}))
		if err == nil {
			t.Errorf("prefix %q loaded; want %s", bad, CodeBadPrefix)
			continue
		}
		wantCodes(t, err, CodeBadPrefix)
	}
	for _, good := range []string{"g", "guild2", "old_ward"} {
		_, err := LoadFS(fsWith(map[string]string{"entity-types.yaml": `
types:
  - name: guild
    prefix: ` + good + `
`}))
		if err != nil {
			t.Errorf("prefix %q: %v", good, err)
		}
	}
}
