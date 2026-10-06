package resolve

import (
	"path/filepath"
	"testing"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/world"
)

// fixtureRepo is the resolver fixture world. Its README lists every entity,
// what it exercises, and the warnings it is meant to raise.
var fixtureRepo = filepath.Join("..", "..", "testdata", "world-resolve")

func TestFixtureBuildsClean(t *testing.T) {
	res, err := index.Load(fixtureRepo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := res.Findings.Errors(); n != 0 {
		t.Fatalf("fixture has %d errors:\n%s", n, res.Findings)
	}
	if res.Index == nil {
		t.Fatal("fixture validated but Load returned no index")
	}

	// The intended warnings, exactly, as the README lists them.
	want := map[world.Code]string{
		"canon_depends_on_draft": "characters/draftling.md",
		"unbelieved_statement":   "statements/heir-common.md",
	}
	got := map[world.Code]string{}
	for _, f := range res.Findings {
		if f.Severity != world.SeverityWarning {
			t.Errorf("unexpected non-warning finding: %s", f)
			continue
		}
		if _, dup := got[f.Code]; dup {
			t.Errorf("warning %s reported more than once: %s", f.Code, f)
		}
		got[f.Code] = f.File
	}
	for code, file := range want {
		if got[code] != file {
			t.Errorf("warning %s: want file %q, got %q", code, file, got[code])
		}
	}
	for code, file := range got {
		if _, ok := want[code]; !ok {
			t.Errorf("unintended warning %s in %s", code, file)
		}
	}
}
