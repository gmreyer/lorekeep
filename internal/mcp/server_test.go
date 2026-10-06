package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fixtureRepo is the resolver fixture world; its README lists what each
// entity exercises. Tests copy it, since the server writes build/.
var fixtureRepo = filepath.Join("..", "..", "testdata", "world-resolve")

// resolveGoldens holds the Step 4 goldens the MCP server must reproduce.
var resolveGoldens = filepath.Join("..", "resolve", "testdata")

// copyWorld copies the fixture into a temp directory and returns its path.
func copyWorld(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"schema", "world"} {
		if err := os.CopyFS(filepath.Join(dir, sub), os.DirFS(filepath.Join(fixtureRepo, sub))); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// connect starts a server over repo and returns a client session to it.
func connect(t *testing.T, repo string) (*Server, *sdk.ClientSession) {
	t.Helper()
	s, err := New(Config{Repo: repo, Role: RoleAuthor})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := s.mcp.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return s, cs
}

// call calls a tool and returns its text and whether it is a tool error.
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// callOK calls a tool that must succeed and decodes its JSON output.
func callOK(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	text, isErr := call(t, cs, name, args)
	if isErr {
		t.Fatalf("%s(%v): tool error: %s", name, args, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: decoding %q: %v", name, text, err)
	}
	return out
}

// callErr calls a tool that must fail and returns the error text.
func callErr(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) string {
	t.Helper()
	text, isErr := call(t, cs, name, args)
	if !isErr {
		t.Fatalf("%s(%v): want a tool error, got %s", name, args, text)
	}
	return text
}

// edit rewrites a file of the copied world and stamps it with a modification
// time step minutes ahead, so a reload is due however coarse the clock.
func edit(t *testing.T, repo, rel string, step int, replace func(string) string) {
	t.Helper()
	path := filepath.Join(repo, "world", filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := replace(string(data))
	if next == string(data) {
		t.Fatalf("edit of %s changed nothing", rel)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Duration(step) * time.Minute)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestToolsListed(t *testing.T) {
	_, cs := connect(t, copyWorld(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)
	want := []string{
		"diff_worldlines", "expand", "get_beliefs", "get_entity",
		"get_world_index", "propose_change", "search", "validate",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tools (-want +got):\n%s", diff)
	}
}

// TestGetEntityThreeContexts returns Step 4's acceptance goldens through MCP:
// the same entity, three contexts, the same content.
func TestGetEntityThreeContexts(t *testing.T) {
	_, cs := connect(t, copyWorld(t))
	cases := []struct {
		golden string
		args   map[string]any
	}{
		{"heir_baseline.json", map[string]any{"id": "char_heir"}},
		{"heir_fell.json", map[string]any{"id": "char_heir", "worldline": map[string]any{"dec_vale": "fell"}}},
		{"heir_kaelen.json", map[string]any{"id": "char_heir", "worldline": map[string]any{"dec_vale": "held"}, "knower": "char_kaelen"}},
	}
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Join(resolveGoldens, c.golden))
		if err != nil {
			t.Fatal(err)
		}
		var want map[string]any
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		got := callOK(t, cs, "get_entity", c.args)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s through MCP (-want +got):\n%s", c.golden, diff)
		}
	}
}

func TestSearchFiltersByWorldline(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)

	ids := func(out map[string]any) []string {
		var got []string
		hits, _ := out["hits"].([]any)
		for _, h := range hits {
			got = append(got, h.(map[string]any)["id"].(string))
		}
		return got
	}

	// loc_vale_keep exists only if the Vale held. Its body is the only text
	// with "held" in it besides decision prose, so it is the hit to watch.
	held := callOK(t, cs, "search", map[string]any{"query": "Vale Keep", "worldline": map[string]any{"dec_vale": "held"}})
	if !slices.Contains(ids(held), "loc_vale_keep") {
		t.Fatalf("held: want loc_vale_keep among hits, got %v", ids(held))
	}
	for _, wl := range []map[string]any{{"dec_vale": "fell"}, {}} {
		out := callOK(t, cs, "search", map[string]any{"query": "Vale Keep", "worldline": wl})
		if slices.Contains(ids(out), "loc_vale_keep") {
			t.Errorf("worldline %v: loc_vale_keep is absent but was returned: %v", wl, ids(out))
		}
		if _, ok := out["dropped"]; ok {
			t.Errorf("worldline %v: dropped hits must not be counted: %v", wl, out)
		}
	}

	// A statement hit is re-read through the resolver too: one that exists
	// only if the Vale fell is not found under held.
	fell := callOK(t, cs, "search", map[string]any{"query": "heir sworn order", "worldline": map[string]any{"dec_vale": "fell"}})
	heldStmt := callOK(t, cs, "search", map[string]any{"query": "heir sworn order", "worldline": map[string]any{"dec_vale": "held"}})
	if !slices.Contains(ids(fell), "stmt_heir_sworn_fell") {
		t.Errorf("fell: want stmt_heir_sworn_fell among hits, got %v", ids(fell))
	}
	if slices.Contains(ids(heldStmt), "stmt_heir_sworn_fell") {
		t.Errorf("held: stmt_heir_sworn_fell is absent but was returned: %v", ids(heldStmt))
	}

	// The search index is rebuilt with everything else after an edit.
	edit(t, repo, "characters/heir.md", 1, func(s string) string {
		return strings.Replace(s, "Subject of the contested statements.", "Subject of the contested statements, and of a moonlit prophecy.", 1)
	})
	after := callOK(t, cs, "search", map[string]any{"query": "moonlit prophecy"})
	if !slices.Equal(ids(after), []string{"char_heir"}) {
		t.Errorf("after edit: want [char_heir], got %v", ids(after))
	}
}

// TestRoleIsTheCeiling checks that no argument widens the session role.
func TestRoleIsTheCeiling(t *testing.T) {
	repo := copyWorld(t)

	t.Run("unknown role fails", func(t *testing.T) {
		if _, err := New(Config{Repo: repo, Role: "reader"}); err == nil {
			t.Error("New with an unknown role: want an error")
		}
		if _, err := New(Config{Repo: repo}); err == nil {
			t.Error("New with no role: want an error")
		}
		if err := Serve(context.Background(), Config{Repo: repo, Role: "reader"}, strings.NewReader(""), &strings.Builder{}); err == nil {
			t.Error("Serve with an unknown role: want an error")
		}
	})

	_, cs := connect(t, repo)

	t.Run("no widening arguments", func(t *testing.T) {
		for _, extra := range []map[string]any{
			{"visibility": "internal"},
			{"visibility": "public"},
			{"omniscient": true},
			{"role": "author"},
		} {
			args := map[string]any{"id": "char_heir", "knower": "char_kaelen"}
			for k, v := range extra {
				args[k] = v
			}
			callErr(t, cs, "get_entity", args)
		}
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range res.Tools {
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			for _, banned := range []string{`"visibility"`, `"omniscient"`, `"role"`} {
				if strings.Contains(string(schema), banned) {
					t.Errorf("%s: input schema has %s: %s", tool.Name, banned, schema)
				}
			}
		}
	})

	t.Run("a knower stays scoped", func(t *testing.T) {
		// Kaelen may not read another agent's mind, whatever else is passed.
		msg := callErr(t, cs, "get_beliefs", map[string]any{"agent": "char_miren", "knower": "char_kaelen"})
		if !strings.Contains(msg, "out of scope") {
			t.Errorf("want an out of scope error, got %q", msg)
		}
		// A scoped diff is refused rather than read omnisciently.
		callErr(t, cs, "diff_worldlines", map[string]any{
			"a": map[string]any{"dec_vale": "held"}, "b": map[string]any{"dec_vale": "fell"}, "knower": "char_kaelen",
		})
	})

	t.Run("statuses are bounded by the role", func(t *testing.T) {
		callOK(t, cs, "get_entity", map[string]any{"id": "fac_draft", "statuses": []any{"canon", "draft"}})
		msg := callErr(t, cs, "get_entity", map[string]any{"id": "fac_draft"})
		if !strings.Contains(msg, "unknown entity") {
			t.Errorf("a draft in a canon read: want unknown entity, got %q", msg)
		}
		for _, bad := range []string{"non_canon", "deprecated", "bogus"} {
			callErr(t, cs, "get_entity", map[string]any{"id": "char_heir", "statuses": []any{bad}})
		}
	})

	t.Run("a role without omniscience needs a knower", func(t *testing.T) {
		const scopedOnly Role = "test_scoped_only"
		roles[scopedOnly] = policy{ceiling: roles[RoleAuthor].ceiling, statuses: roles[RoleAuthor].statuses}
		t.Cleanup(func() { delete(roles, scopedOnly) })

		s, err := New(Config{Repo: repo, Role: scopedOnly})
		if err != nil {
			t.Fatal(err)
		}
		ct, st := sdk.NewInMemoryTransports()
		ss, err := s.mcp.Connect(context.Background(), st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()
		scs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(context.Background(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer scs.Close()

		for _, tool := range []string{"get_entity", "expand"} {
			msg := callErr(t, scs, tool, map[string]any{"id": "char_heir"})
			if !strings.Contains(msg, "knower") {
				t.Errorf("%s without a knower: want a knower error, got %q", tool, msg)
			}
		}
		callErr(t, scs, "get_world_index", map[string]any{})
		callErr(t, scs, "search", map[string]any{"query": "heir"})
		callOK(t, scs, "get_entity", map[string]any{"id": "char_heir", "knower": "char_kaelen"})
	})
}

func TestReloadOnChange(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)

	if got := callOK(t, cs, "get_entity", map[string]any{"id": "char_heir"})["name"]; got != "The Heir" {
		t.Fatalf("before: name %v", got)
	}
	edit(t, repo, "characters/heir.md", 1, func(s string) string {
		return strings.Replace(s, "name: The Heir", "name: The Lost Heir", 1)
	})
	if got := callOK(t, cs, "get_entity", map[string]any{"id": "char_heir"})["name"]; got != "The Lost Heir" {
		t.Errorf("after edit: name %v, want The Lost Heir", got)
	}

	// A removed file is a change too: the directory's time moves.
	if err := os.Remove(filepath.Join(repo, "world", "objects", "horn.md")); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(filepath.Join(repo, "world", "objects"), at, at); err != nil {
		t.Fatal(err)
	}
	msg := callErr(t, cs, "get_entity", map[string]any{"id": "obj_horn"})
	if !strings.Contains(msg, "unknown entity") {
		t.Errorf("after removal: want unknown entity, got %q", msg)
	}
}

func TestBrokenEditKeepsLastGoodIndex(t *testing.T) {
	repo := copyWorld(t)
	_, cs := connect(t, repo)

	// A dangling target is a blocking error.
	edit(t, repo, "characters/kaelen.md", 1, func(s string) string {
		return strings.Replace(s, "target: fac_order", "target: fac_nowhere", 1)
	})
	out := callOK(t, cs, "get_entity", map[string]any{"id": "char_kaelen"})
	if out["name"] != "Kaelen" {
		t.Errorf("broken edit: want the last good Kaelen, got %v", out)
	}
	edges, _ := out["edges"].([]any)
	if !strings.Contains(mustJSON(t, edges), "fac_order") {
		t.Errorf("broken edit: want the last good edges, got %v", edges)
	}
	findings, _ := out["build_findings"].([]any)
	if len(findings) == 0 || !strings.Contains(mustJSON(t, findings), "world/characters/kaelen.md") {
		t.Errorf("broken edit: want findings naming world/characters/kaelen.md, got %v", out["build_findings"])
	}

	// Every read carries the findings, tool errors included.
	msg := callErr(t, cs, "get_entity", map[string]any{"id": "char_nobody"})
	if !strings.Contains(msg, "kaelen.md") {
		t.Errorf("tool error during a failed build: want the findings, got %q", msg)
	}

	// Fixing the file clears them and serves the new world.
	edit(t, repo, "characters/kaelen.md", 2, func(s string) string {
		s = strings.Replace(s, "target: fac_nowhere", "target: fac_order", 1)
		return strings.Replace(s, "name: Kaelen", "name: Kaelen the Twice-Sworn", 1)
	})
	out = callOK(t, cs, "get_entity", map[string]any{"id": "char_kaelen"})
	if _, ok := out["build_findings"]; ok {
		t.Errorf("after the fix: want no findings, got %v", out["build_findings"])
	}
	if out["name"] != "Kaelen the Twice-Sworn" {
		t.Errorf("after the fix: want the new name, got %v", out["name"])
	}
}

func TestWorldIndex(t *testing.T) {
	_, cs := connect(t, copyWorld(t))
	out := callOK(t, cs, "get_world_index", map[string]any{})

	decisions := mustJSON(t, out["decisions"])
	if !strings.HasPrefix(decisions, `[{"id":"dec_heir",`) || !strings.Contains(decisions, `"outcomes":["held","fell"]`) {
		t.Errorf("decisions: %s", decisions)
	}
	types := mustJSON(t, out["types"])
	if strings.Contains(types, "loc_vale_keep") {
		t.Errorf("empty worldline: loc_vale_keep is absent but listed: %s", types)
	}
	if !strings.Contains(types, `"type":"character"`) {
		t.Errorf("types: %s", types)
	}
	if rels, _ := out["relations"].([]any); len(rels) == 0 {
		t.Errorf("relations: want the vocabulary, got %v", out["relations"])
	}

	held := callOK(t, cs, "get_world_index", map[string]any{"worldline": map[string]any{"dec_vale": "held"}, "knower": "char_kaelen"})
	if !strings.Contains(mustJSON(t, held["types"]), "loc_vale_keep") {
		t.Errorf("held: want loc_vale_keep listed")
	}
}

// TestServe speaks to Serve over a pair of pipes, as a client does over
// stdio.
func TestServe(t *testing.T) {
	repo := copyWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, Config{Repo: repo, Role: RoleAuthor}, inR, outW) }()

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, &sdk.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got := callOK(t, cs, "get_entity", map[string]any{"id": "char_heir"})["name"]; got != "The Heir" {
		t.Errorf("name %v", got)
	}
	_ = cs.Close()
	_ = inW.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its input closed")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
