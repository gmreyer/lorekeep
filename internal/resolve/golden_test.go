package resolve

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// update regenerates the goldens. As in internal/index, accepting a golden is
// a decision: a diff here can be a spoiler leaking into a scoped read.
var update = flag.Bool("update", false, "regenerate the golden files in testdata/")

// TestOneEntityThreeContexts is Step 4's acceptance test: one entity renders
// three ways under three contexts.
//
//   - heir_baseline: canon truth in every branch's common ground
//   - heir_fell: canon truth once the Vale fell, which adds a statement
//   - heir_kaelen: what Kaelen believes about the heir if the Vale held —
//     his court's view, common knowledge, and nothing he is ignorant of
func TestOneEntityThreeContexts(t *testing.T) {
	r := fixture(t)
	cases := []struct {
		golden string
		ctx    Context
	}{
		{"heir_baseline.json", Omniscient(nil)},
		{"heir_fell.json", Omniscient(Worldline{"dec_vale": "fell"})},
		{"heir_kaelen.json", Scoped(Worldline{"dec_vale": "held"}, "char_kaelen")},
	}
	rendered := map[string]string{}
	for _, c := range cases {
		e, err := r.Entity(c.ctx, "char_heir")
		if err != nil {
			t.Fatalf("%s: %v", c.golden, err)
		}
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got := string(data) + "\n"
		rendered[c.golden] = got
		compareGolden(t, c.golden, got)
	}
	for i, a := range cases {
		for _, b := range cases[i+1:] {
			if rendered[a.golden] == rendered[b.golden] {
				t.Errorf("%s and %s render identically", a.golden, b.golden)
			}
		}
	}
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (run go test -update to create it): %v", path, err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s is out of date (-want +got):\n%s", name, diff)
	}
}
