package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// kaelenPlanted is char_kaelen with a belief on a statement that exists only
// under dec_vale=fell, learned from someone who does not exist.
const kaelenPlanted = `---
id: char_kaelen
type: character
name: Kaelen
status: canon
visibility: public
relations:
  - { type: member_of, target: fac_court, priority: 1 }
  - { type: member_of, target: fac_order, priority: 2 }
beliefs:
  - { statement: stmt_heir_sworn_fell, value: true, acquired_from: char_nobody }
---

Member of two factions that disagree; now believes the heir sworn to the Order.
`

// newcomer is a valid new entity.
const newcomer = `---
id: char_newcomer
type: character
name: Newcomer
status: canon
visibility: public
relations:
  - { type: member_of, target: fac_court }
---

A new face at court.
`

// linkDir makes link a link to the folder target: a symlink, or on Windows
// without Developer Mode a junction, which needs no privilege. It skips the
// test when neither can be made.
func linkDir(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		out, jerr := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if jerr == nil {
			return
		}
		t.Skipf("cannot link a folder here: %v; mklink /J: %v %s", err, jerr, out)
	}
	t.Skipf("cannot link a folder here: %v", err)
}

// hashTree hashes every file and directory under root, by slash path.
func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		if !d.Type().IsRegular() {
			out[rel] = "link"
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// hashAuthored hashes world/ and schema/ of a project.
func hashAuthored(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, sub := range []string{"world", "schema"} {
		for k, v := range hashTree(t, filepath.Join(repo, sub)) {
			out[sub+"/"+k] = v
		}
	}
	return out
}

func files(pairs ...string) []any {
	var out []any
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, map[string]any{"path": pairs[i], "content": pairs[i+1]})
	}
	return out
}

func TestProposalPathsConfined(t *testing.T) {
	repo := copyWorld(t)
	s, cs := connect(t, repo)
	before := hashAuthored(t, repo)

	bad := []string{
		"../x.md",
		"world/../../x.md",
		"world/../schema/x.md",
		"C:/x.md",
		"/x.md",
		"schema/pack.yaml",
		"schema/x.md",
		"world/a.txt",
		"world\\characters\\a.md",
		"x.md",
		"world/.hidden/a.md",
		"world/build/a.md",
		"world//a.md",
		"world/./a.md",
		"",
	}
	for _, p := range bad {
		for _, tool := range []string{"validate", "propose_change"} {
			args := map[string]any{"files": files(p, newcomer), "summary": "add a newcomer"}
			callErr(t, cs, tool, args)
		}
		// One bad path rejects the whole call, good files included.
		callErr(t, cs, "propose_change", map[string]any{
			"files":   files("world/characters/newcomer.md", newcomer, p, newcomer),
			"summary": "add a newcomer",
		})
	}

	t.Run("symlinked folder", func(t *testing.T) {
		outside := t.TempDir()
		linkDir(t, outside, filepath.Join(repo, "world", "link"))
		before := hashAuthored(t, repo)
		for _, tool := range []string{"validate", "propose_change"} {
			callErr(t, cs, tool, map[string]any{"files": files("world/link/a.md", newcomer), "summary": "escape"})
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("wrote outside the project: %v", entries)
		}
		if diff := cmp.Diff(before, hashAuthored(t, repo)); diff != "" {
			t.Errorf("authored files changed (-before +after):\n%s", diff)
		}
	})

	if _, err := os.Stat(filepath.Join(repo, "proposals")); !os.IsNotExist(err) {
		t.Errorf("a rejected call wrote proposals/: %v", err)
	}
	if diff := cmp.Diff(before, hashAuthored(t, repo)); diff != "" && !strings.Contains(diff, "world/link") {
		t.Errorf("authored files changed (-before +after):\n%s", diff)
	}

	// The confinement is in the server, not only in the tool layer.
	if _, err := s.propose(context.Background(), []ProposedFile{{Path: "../x.md", Content: newcomer}}, "x"); err == nil {
		t.Error("propose(../x.md): want an error")
	}
}

func TestProposalRejectsNonEntityFiles(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)
	for _, content := range []string{
		"no frontmatter at all\n",
		"---\nid: [unclosed\n---\n",
		"---\n---\nempty\n",
	} {
		for _, tool := range []string{"validate", "propose_change"} {
			callErr(t, cs, tool, map[string]any{"files": files("world/characters/x.md", content), "summary": "x"})
		}
	}
	callErr(t, cs, "propose_change", map[string]any{"files": files("world/characters/n.md", newcomer), "summary": ""})
	callErr(t, cs, "validate", map[string]any{"files": []any{}})
	// The same path twice, even in another case, is ambiguous.
	callErr(t, cs, "validate", map[string]any{"files": files("world/characters/n.md", newcomer, "world/Characters/N.md", newcomer)})
}

func TestValidateCatchesPlantedContradiction(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)
	before := hashAuthored(t, repo)

	out := callOK(t, cs, "validate", map[string]any{"files": files("world/characters/kaelen.md", kaelenPlanted)})

	var res validateOut
	remarshal(t, out, &res)

	var dangling bool
	for _, f := range res.Findings {
		if f.Code == "dangling_reference" && f.File == "world/characters/kaelen.md" && f.Severity == "error" {
			dangling = true
		}
	}
	if !dangling {
		t.Errorf("want a dangling_reference error in world/characters/kaelen.md, got %+v", res.Findings)
	}
	if !res.Blocking {
		t.Error("blocking: want true")
	}

	i := slices.IndexFunc(res.Neighbourhood, func(g graphOut) bool { return g.Root == "char_kaelen" })
	if i < 0 {
		t.Fatalf("no neighbourhood rooted at char_kaelen: %+v", res.Neighbourhood)
	}
	var nodes []string
	for _, n := range res.Neighbourhood[i].Nodes {
		nodes = append(nodes, n.ID)
	}
	for _, want := range []string{"fac_court", "fac_order"} {
		if !slices.Contains(nodes, want) {
			t.Errorf("neighbourhood of char_kaelen: want %s among %v", want, nodes)
		}
	}

	if diff := cmp.Diff(before, hashAuthored(t, repo)); diff != "" {
		t.Errorf("validate changed authored files (-before +after):\n%s", diff)
	}
	if _, err := os.Stat(filepath.Join(repo, "proposals")); !os.IsNotExist(err) {
		t.Errorf("validate wrote proposals/: %v", err)
	}
}

func TestValidateCleanProposal(t *testing.T) {
	_, cs := connect(t, copyWorld(t))
	out := callOK(t, cs, "validate", map[string]any{"files": files("world/characters/newcomer.md", newcomer)})
	var res validateOut
	remarshal(t, out, &res)
	if res.Blocking {
		t.Errorf("a clean proposal is blocking: %+v", res.Findings)
	}
	i := slices.IndexFunc(res.Neighbourhood, func(g graphOut) bool { return g.Root == "char_newcomer" })
	if i < 0 {
		t.Fatalf("no neighbourhood for the new entity: %+v", res.Neighbourhood)
	}
}

// TestInvalidProposalsKeepServing sends worlds that fail validation in ways
// the resolver never sees from a good build. Each call returns findings, and
// the server keeps answering.
func TestInvalidProposalsKeepServing(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)
	cases := []struct{ name, path, content string }{
		{"unknown relation", "world/characters/kaelen.md", strings.Replace(kaelenPlanted,
			"{ type: member_of, target: fac_court, priority: 1 }", "{ type: haunts_badly, target: fac_court }", 1)},
		{"unknown entity type", "world/characters/dragon.md", strings.NewReplacer(
			"char_newcomer", "char_dragon", "type: character", "type: dragon").Replace(newcomer)},
		{"duplicate id", "world/characters/kaelen2.md", strings.Replace(newcomer, "char_newcomer", "char_kaelen", 1)},
		{"statement with unknown subject", "world/statements/ghost.md", `---
id: stmt_ghost_sworn
type: statement
name: A ghost is sworn to the court
subject: char_ghost
relation: sworn_to
object: fac_court
truth: true
status: canon
visibility: public
---
`},
	}
	for _, c := range cases {
		for _, tool := range []string{"validate", "propose_change"} {
			args := map[string]any{"files": files(c.path, c.content)}
			if tool == "propose_change" {
				args["summary"] = c.name
			}
			out := callOK(t, cs, tool, args)
			var res validateOut
			remarshal(t, out, &res)
			if !res.Blocking || len(res.Findings) == 0 {
				t.Errorf("%s via %s: want blocking findings, got %+v", c.name, tool, out)
			}
			callOK(t, cs, "get_entity", map[string]any{"id": "char_kaelen"})
		}
	}
}

// TestNeighbourhoodRecoversOnlyUnvalidated forces the resolver to panic with
// a nil index: over an unvalidated world the panic becomes a note, over a
// validated one it is not hidden.
func TestNeighbourhoodRecoversOnlyUnvalidated(t *testing.T) {
	s, _ := connect(t, copyWorld(t))
	graphs, note := s.neighbourhood(overlay{unvalidated: true, roots: []string{"char_kaelen"}})
	if len(graphs) != 0 || note != noNeighbourhood {
		t.Errorf("unvalidated panic: got %v, %q", graphs, note)
	}
	defer func() {
		if recover() == nil {
			t.Error("a panic over a validated index was recovered")
		}
	}()
	s.neighbourhood(overlay{roots: []string{"char_kaelen"}})
}

var proposalID = regexp.MustCompile(`^p_\d{8}_\d{6}_[0-9a-f]{4}$`)

func TestProposeWritesProposalOnly(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)
	before := hashAuthored(t, repo)

	out := callOK(t, cs, "propose_change", map[string]any{
		"files":   files("world/characters/kaelen.md", kaelenPlanted, "world/characters/newcomer.md", newcomer),
		"summary": "Kaelen learns of the heir's oath; a newcomer joins the court",
	})
	var res ProposeResult
	remarshal(t, out, &res)

	if !proposalID.MatchString(res.ID) {
		t.Fatalf("id %q does not match %s", res.ID, proposalID)
	}
	if !res.Blocking || res.Note == "" {
		t.Errorf("a proposal with blocking findings must say so: %+v", res)
	}
	if res.Dir != "proposals/"+res.ID {
		t.Errorf("dir = %q", res.Dir)
	}

	if diff := cmp.Diff(before, hashAuthored(t, repo)); diff != "" {
		t.Errorf("propose_change changed authored files (-before +after):\n%s", diff)
	}

	// Only build/ (the server's) and proposals/ are new at the top.
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	var top []string
	for _, e := range entries {
		top = append(top, e.Name())
	}
	if diff := cmp.Diff([]string{"build", "proposals", "schema", "world"}, top); diff != "" {
		t.Errorf("project top level (-want +got):\n%s", diff)
	}

	dir := filepath.Join(repo, "proposals", res.ID)
	got := hashTree(t, dir)
	var paths []string
	for p := range got {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	want := []string{
		"./", "proposal.json", "validation.txt",
		"world/", "world/characters/", "world/characters/kaelen.md", "world/characters/newcomer.md",
	}
	if diff := cmp.Diff(want, paths); diff != "" {
		t.Errorf("proposal files (-want +got):\n%s", diff)
	}

	data, err := os.ReadFile(filepath.Join(dir, "world", "characters", "kaelen.md"))
	if err != nil || string(data) != kaelenPlanted {
		t.Errorf("kaelen.md is not the proposed file: %v %q", err, data)
	}

	var meta map[string]any
	data, err = os.ReadFile(filepath.Join(dir, "proposal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["summary"] != "Kaelen learns of the heir's oath; a newcomer joins the court" {
		t.Errorf("summary = %v", meta["summary"])
	}
	if diff := cmp.Diff([]any{"world/characters/kaelen.md", "world/characters/newcomer.md"}, meta["files"]); diff != "" {
		t.Errorf("files (-want +got):\n%s", diff)
	}
	for _, k := range []string{"created", "lorekeep_version"} {
		if v, _ := meta[k].(string); v == "" {
			t.Errorf("proposal.json: %s missing: %v", k, meta)
		}
	}

	data, err = os.ReadFile(filepath.Join(dir, "validation.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "error   world/characters/kaelen.md") || !strings.Contains(text, "[dangling_reference]") {
		t.Errorf("validation.txt does not report the dangling reference:\n%s", text)
	}

	// A second proposal never reuses the first's directory.
	again := callOK(t, cs, "propose_change", map[string]any{
		"files": files("world/characters/newcomer.md", newcomer), "summary": "a newcomer",
	})
	var res2 ProposeResult
	remarshal(t, again, &res2)
	if res2.ID == res.ID {
		t.Errorf("proposal id reused: %s", res.ID)
	}
	if res2.Blocking {
		t.Errorf("a clean proposal is blocking: %+v", res2)
	}
}

func TestProposeRefusesSymlinkedProposals(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)
	linkDir(t, filepath.Join(repo, "world"), filepath.Join(repo, "proposals"))
	before := hashAuthored(t, repo)
	callErr(t, cs, "propose_change", map[string]any{"files": files("world/characters/newcomer.md", newcomer), "summary": "x"})
	if diff := cmp.Diff(before, hashAuthored(t, repo)); diff != "" {
		t.Errorf("authored files changed (-before +after):\n%s", diff)
	}
}

// TestNothingWritesWorld runs every tool through the SDK and checks that
// world/ and schema/ are byte for byte what they were.
func TestNothingWritesWorld(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)
	before := hashAuthored(t, repo)

	callOK(t, cs, "get_world_index", map[string]any{})
	callOK(t, cs, "search", map[string]any{"query": "heir"})
	callOK(t, cs, "get_entity", map[string]any{"id": "char_kaelen", "knower": "char_kaelen"})
	callOK(t, cs, "expand", map[string]any{"id": "char_kaelen", "depth": 2})
	callOK(t, cs, "get_beliefs", map[string]any{"agent": "fac_court"})
	callOK(t, cs, "diff_worldlines", map[string]any{"a": map[string]any{"dec_vale": "held"}, "b": map[string]any{"dec_vale": "fell"}})
	callOK(t, cs, "validate", map[string]any{"files": files("world/characters/kaelen.md", kaelenPlanted)})
	callOK(t, cs, "propose_change", map[string]any{"files": files("world/characters/kaelen.md", kaelenPlanted), "summary": "plant"})
	callOK(t, cs, "propose_change", map[string]any{"files": files("world/characters/newcomer.md", newcomer), "summary": "add"})
	callErr(t, cs, "propose_change", map[string]any{"files": files("world/../world/characters/kaelen.md", newcomer), "summary": "x"})

	if diff := cmp.Diff(before, hashAuthored(t, repo)); diff != "" {
		t.Errorf("a tool changed authored files (-before +after):\n%s", diff)
	}
}

// validateOut is a validate result as the client decodes it.
type validateOut struct {
	Findings      []Finding  `json:"findings"`
	Blocking      bool       `json:"blocking"`
	Neighbourhood []graphOut `json:"neighbourhood"`
}

// graphOut is a neighbourhood as the client decodes it.
type graphOut = struct {
	Root  string `json:"root"`
	Nodes []struct {
		ID string `json:"id"`
	} `json:"nodes"`
}

func remarshal(t *testing.T, in any, out any) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decoding %s: %v", data, err)
	}
}
