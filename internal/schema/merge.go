package schema

import (
	"fmt"
	"slices"
	"strings"
)

// Merge folds a project pack over the core pack and returns the world's
// complete vocabulary.
//
// A name declared in both packs is an error — never an override, never a
// shadow. That is deliberate. Promotion upstream is the point of the two-tier
// design: a project relation that proves general moves into core in a later
// version. If the project silently won, the day core adopted the name the
// project would keep its own copy forever, never notice, and the two
// definitions would drift. The error is the notification that promotion
// happened, and it arrives at the core upgrade, which is exactly when the
// release's rename map is the tool for it.
//
// Neither input is modified.
func Merge(core, project *Pack) (*Pack, error) {
	l := &errList{source: SourceProject}

	checkTierRules(l, core, project)
	notices := checkCoreVersion(l, core, project)

	merged := &Pack{
		Name:        project.Name,
		Version:     project.Version,
		CoreVersion: project.CoreVersion,
		Description: project.Description,
		Source:      SourceMerged,
		Notices:     notices,

		// Core first, then the project's own, each keeping file order.
		Types:     concat(core.Types, project.Types),
		Groups:    concat(core.Groups, project.Groups),
		Roles:     concat(core.Roles, project.Roles),
		Relations: concatRelations(core.Relations, project.Relations),
		Eras:      concat(core.Eras, project.Eras),
		Acts:      concat(core.Acts, project.Acts),
	}

	checkCollisions(l, core, project)

	// index before the cross-reference checks: they read the resolved tables.
	merged.index()
	checkGroups(l, merged, core, project)
	checkCrossReferences(l, merged, project)

	if err := l.err(); err != nil {
		return nil, err
	}
	return merged, nil
}

// checkTierRules enforces what belongs in which pack: eras and acts are world
// content, and roles are core-owned.
func checkTierRules(l *errList, core, project *Pack) {
	for i, e := range core.Eras {
		l.addFrom(SourceCore, CodeEraInCore, erasFile, fmt.Sprintf("eras[%d]", i),
			"the core pack declares era %q; eras are world content and belong to a project pack",
			e.Key)
	}
	for i, a := range core.Acts {
		l.addFrom(SourceCore, CodeActInCore, actsFile, fmt.Sprintf("acts[%d]", i),
			"the core pack declares act %q; acts are world content and belong to a project pack",
			a.Key)
	}
	for i, r := range project.Roles {
		l.add(CodeRoleInProject, relationsFile, fmt.Sprintf("roles[%d]", i),
			"project pack declares role %q; roles are core-owned because each one binds to a view",
			r.Name)
	}
}

// checkCoreVersion compares the project's pin against the core pack actually
// present. A newer core is a notice, not an error: the project still loads,
// and the release's rename map is the tool for the upgrade.
func checkCoreVersion(l *errList, core, project *Pack) []Notice {
	pin, err := parseVersion(project.CoreVersion)
	if err != nil {
		l.add(CodeBadVersion, packFile, "core_version", "%s", err)
		return nil
	}
	have, err := parseVersion(core.Version)
	if err != nil {
		l.addFrom(SourceCore, CodeBadVersion, packFile, "version", "%s", err)
		return nil
	}

	switch {
	case pin.major != have.major:
		l.add(CodeCoreMajor, packFile, "core_version",
			"pack was authored against core %s but core %s is present; "+
				"a major change is not mechanical, so read the release's rename map",
			pin, have)
	case have.compare(pin) < 0:
		l.add(CodeCoreTooOld, packFile, "core_version",
			"pack was authored against core %s but only core %s is present; "+
				"update the lorekeep requirement in go.mod", pin, have)
	case have.compare(pin) > 0:
		return []Notice{{
			Code: CodeCoreNewer,
			Msg: fmt.Sprintf(
				"pack was authored against core %s and core %s is present; "+
					"review the release's rename map, then update core_version", pin, have),
		}}
	}
	return nil
}

// checkCollisions reports any name the project claims that core already holds.
func checkCollisions(l *errList, core, project *Pack) {
	// Relation names and derived inverse names are one namespace.
	relNS := newCrossNamespace()
	for _, r := range core.Relations {
		relNS.hold(r.Name, "relation")
		if r.Inverse != "" {
			relNS.hold(r.Inverse, fmt.Sprintf("inverse of %s", r.Name))
		}
	}
	for i, r := range project.Relations {
		base := fmt.Sprintf("relations[%d]", i)
		relNS.check(l, relationsFile, base+".name", r.Name, "relation")
		if r.Inverse != "" {
			relNS.check(l, relationsFile, base+".inverse", r.Inverse,
				fmt.Sprintf("inverse of %s", r.Name))
		}
	}

	// Entity types and groups are one namespace.
	typeNS := newCrossNamespace()
	for _, t := range core.Types {
		typeNS.hold(t.Name, "entity type")
	}
	for _, g := range core.Groups {
		typeNS.hold(g.Name, "group")
	}
	for i, t := range project.Types {
		typeNS.check(l, typesFile, fmt.Sprintf("types[%d].name", i), t.Name, "entity type")
	}
	for i, g := range project.Groups {
		typeNS.check(l, typesFile, fmt.Sprintf("groups[%d].name", i), g.Name, "group")
	}
}

// checkGroups reports groups that include something undeclared, and groups
// that cycle. Expansion treats a cycle as empty, so this is what surfaces it.
func checkGroups(l *errList, merged, core, project *Pack) {
	known := make(map[string]struct{}, len(merged.Groups))
	for _, g := range merged.Groups {
		known[g.Name] = struct{}{}
	}
	expansion := merged.groupExpansion()

	for _, g := range merged.Groups {
		file, path, src := locateGroup(g.Name, core, project)
		for _, inc := range g.Includes {
			if inc == wildcard || merged.HasType(inc) {
				continue
			}
			if _, ok := known[inc]; !ok {
				l.addFrom(src, CodeUnknownType, file, path,
					"group %q includes %q, which is neither an entity type nor a group",
					g.Name, inc)
			}
		}
		// A group that includes only groups and resolves to nothing is either
		// a cycle or made of cycles; an unknown member is already reported.
		if len(expansion[g.Name]) == 0 && allGroups(g.Includes, known) {
			l.addFrom(src, CodeGroupCycle, file, path,
				"group %q resolves to no entity type; its includes form a cycle", g.Name)
		}
	}
}

// checkCrossReferences validates every relation's domain, range, and role
// against the merged vocabulary. Only the project's relations are reported by
// path, since core's have already been checked by its own tests.
func checkCrossReferences(l *errList, merged, project *Pack) {
	known := make(map[string]struct{}, len(merged.Groups))
	for _, g := range merged.Groups {
		known[g.Name] = struct{}{}
	}

	for _, r := range merged.Relations {
		src, file, base := SourceCore, relationsFile, "relations."+r.Name
		if i := slices.IndexFunc(project.Relations, func(p Relation) bool { return p.Name == r.Name }); i >= 0 {
			src, base = SourceProject, fmt.Sprintf("relations[%d]", i)
		}

		for _, field := range []struct {
			name  string
			names []string
		}{{"domain", r.Domain}, {"range", r.Range}} {
			for _, n := range field.names {
				if merged.HasType(n) {
					continue
				}
				if _, ok := known[n]; ok {
					continue
				}
				l.addFrom(src, CodeUnknownType, file, base+"."+field.name,
					"relation %q names %q in its %s, which is neither an entity type nor a group",
					r.Name, n, field.name)
			}
		}

		if r.Role != "" && !merged.HasRole(r.Role) {
			l.addFrom(src, CodeUnknownRole, file, base+".role",
				"relation %q carries role %q, which the core pack does not declare; known roles: %s",
				r.Name, r.Role, strings.Join(roleNames(merged), ", "))
		}
	}
}

// crossNamespace holds the names core has already claimed and reports a
// project name that lands on one.
type crossNamespace struct {
	exact map[string]string
	fold  map[string]string
}

func newCrossNamespace() *crossNamespace {
	return &crossNamespace{exact: map[string]string{}, fold: map[string]string{}}
}

func (n *crossNamespace) hold(name, kind string) {
	n.exact[name] = kind
	n.fold[strings.ToLower(name)] = name
}

func (n *crossNamespace) check(l *errList, file, path, name, kind string) {
	if prev, ok := n.exact[name]; ok {
		l.add(CodeCollision, file, path,
			"%s %q is already declared by the core pack as a %s; "+
				"drop the local declaration if core has adopted it, or rename it",
			kind, name, prev)
		return
	}
	if prev, ok := n.fold[strings.ToLower(name)]; ok {
		l.add(CodeCaseCollision, file, path,
			"%s %q collides with the core pack's %q when case is ignored", kind, name, prev)
	}
}

func locateGroup(name string, core, project *Pack) (file, path string, src Source) {
	if i := slices.IndexFunc(project.Groups, func(g Group) bool { return g.Name == name }); i >= 0 {
		return typesFile, fmt.Sprintf("groups[%d]", i), SourceProject
	}
	i := slices.IndexFunc(core.Groups, func(g Group) bool { return g.Name == name })
	return typesFile, fmt.Sprintf("groups[%d]", i), SourceCore
}

func allGroups(includes []string, known map[string]struct{}) bool {
	for _, inc := range includes {
		if _, ok := known[inc]; !ok {
			return false
		}
	}
	return len(includes) > 0
}

func roleNames(p *Pack) []string {
	out := make([]string, 0, len(p.Roles))
	for _, r := range p.Roles {
		out = append(out, string(r.Name))
	}
	slices.Sort(out)
	return out
}

func concat[T any](a, b []T) []T {
	out := make([]T, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

func concatRelations(a, b []Relation) []Relation {
	out := make([]Relation, 0, len(a)+len(b))
	for _, r := range a {
		out = append(out, cloneRelation(r))
	}
	for _, r := range b {
		out = append(out, cloneRelation(r))
	}
	return out
}
