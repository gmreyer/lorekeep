package schema

import (
	"slices"
	"testing"
)

func TestCoreLoads(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatalf("Core(): %v", err)
	}
	if c.Name != "core" {
		t.Errorf("Name = %q, want %q", c.Name, "core")
	}
	if c.Source != SourceCore {
		t.Errorf("Source = %q, want %q", c.Source, SourceCore)
	}
	if _, err := parseVersion(c.Version); err != nil {
		t.Errorf("Version %q is not semver: %v", c.Version, err)
	}
	if c.CoreVersion != "" {
		t.Errorf("core pack pins a core version (%q); it cannot pin itself", c.CoreVersion)
	}
}

// The core pack is project-independent, and an era is world content. The core
// pack therefore declares none, and Merge rejects one that does.
func TestCoreDeclaresNoEras(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Eras) != 0 {
		t.Errorf("core pack declares %d era(s); it must declare none", len(c.Eras))
	}
}

// Roles are the binding surface for views and are core-owned. This pins the
// closed set: adding one is a deliberate core change, and this test is the
// place that records the decision.
func TestCoreRoleSet(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	want := []Role{
		"causation", "containment", "identity", "membership",
		"ordering", "ownership", "participation", "placement",
	}
	var got []Role
	for _, r := range c.Roles {
		got = append(got, r.Name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("core roles = %v, want %v", got, want)
	}
}

// Core holds structural relations only. This guards against world-flavoured
// vocabulary drifting upstream by accident.
func TestCoreHasNoWorldFlavouredRelations(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{
		"allied_with", "hostile_to", "sworn_to", "bound_by_oath",
		"practises", "forbids", "descended_from_line", "worships",
	}
	for _, rel := range c.Relations {
		if slices.Contains(banned, rel.Name) {
			t.Errorf("core declares world-flavoured relation %q; it belongs in a project pack", rel.Name)
		}
	}
}

// Every structural category named in the spec is represented, and every core
// relation that carries a role carries a declared one.
func TestCoreCoversStructuralRoles(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []Role{
		"identity", "membership", "containment", "placement",
		"participation", "causation", "ownership", "ordering",
	} {
		if len(c.RelationsWithRole(want)) == 0 {
			t.Errorf("no core relation carries role %q", want)
		}
	}
}

// Core must be internally consistent on its own terms: every domain and range
// resolves, every role is declared, every inverse is unique.
func TestCoreIsSelfConsistent(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	empty, err := LoadFS(fsWith(nil))
	if err != nil {
		t.Fatalf("loading empty project pack: %v", err)
	}
	if _, err := Merge(c, empty); err != nil {
		t.Fatalf("core pack merged with an empty project pack: %v", err)
	}
}

// occurred_at anchors an event to a place without claiming anything is inside
// the event, so it is placement, not containment. Containment is reserved for
// the transitive hierarchy that traversal walks.
func TestOccurredAtIsPlacementNotContainment(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	rel, ok := c.Relation("occurred_at")
	if !ok {
		t.Fatal("core does not declare occurred_at")
	}
	if rel.Role != "placement" {
		t.Errorf("occurred_at role = %q, want %q", rel.Role, "placement")
	}
	for _, r := range c.RelationsWithRole("containment") {
		if r.Domain[0] == "event" {
			t.Errorf("%q is tagged containment with an event domain; "+
				"events are placed, not contained", r.Name)
		}
	}
}

// Anything named in Go must still be declared by the pack. If core ever drops
// one of these, the rules that bind to it would quietly stop firing rather
// than fail, which is the worst way for a validator to break.
func TestCoreDeclaresNamedVocabulary(t *testing.T) {
	c, err := Core()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{TypeCharacter, TypeEvent, TypeDecision} {
		if !c.HasType(name) {
			t.Errorf("core does not declare entity type %q, which lorekeep names in code", name)
		}
	}
	for _, role := range []Role{RoleParticipation, RoleContainment, RoleMembership, RoleOrdering} {
		if !c.HasRole(role) {
			t.Errorf("core does not declare role %q, which lorekeep names in code", role)
		}
		if len(c.RelationsWithRole(role)) == 0 {
			t.Errorf("no core relation carries role %q", role)
		}
	}
}
