package validate

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gmreyer/lore-core/internal/schema"
	"github.com/gmreyer/lore-core/internal/world"
)

// fixtureRepo is the shared world repo: schema pack and world content, read by
// this package, the index builder, and the CLI. One copy means one place where
// the format and the vocabulary have to agree.
var fixtureRepo = filepath.Join("..", "..", "testdata", "world-ok")

// check copies the fixture repo to a temp directory, applies an overlay, and
// validates the result.
//
// Each case is one fault applied to a world that is otherwise correct, which
// is the only way to be sure a finding came from the fault and not from the
// fixture. An overlay value of "" deletes the file.
func check(t *testing.T, overlay map[string]string) world.Findings {
	t.Helper()
	root := copyRepo(t, fixtureRepo)

	for rel, body := range overlay {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if body == "" {
			if err := os.Remove(p); err != nil {
				t.Fatalf("overlay delete %s: %v", rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return validateRepo(t, root)
}

// checkMutated validates the fixture after an in-memory change to the loaded
// world.
//
// It exists for one rule: two filenames colliding when case is ignored cannot
// be written to a temp directory on Windows, because Windows is exactly the
// platform that cannot hold both — which is why the rule exists at all. The
// collision is therefore built in memory rather than on disk.
func checkMutated(t *testing.T, mutate func(*world.World)) world.Findings {
	t.Helper()
	root := copyRepo(t, fixtureRepo)

	pack := loadPack(t, root)
	w, parseFindings := world.Load(filepath.Join(root, "world"))
	if len(parseFindings) != 0 {
		t.Fatalf("fixture did not parse: %v", world.Findings(parseFindings))
	}
	mutate(w)
	return Validate(w, pack)
}

func validateRepo(t *testing.T, root string) world.Findings {
	t.Helper()
	pack := loadPack(t, root)
	w, parseFindings := world.Load(filepath.Join(root, "world"))
	return append(world.Findings(parseFindings), Validate(w, pack)...)
}

func loadPack(t *testing.T, root string) *schema.Pack {
	t.Helper()
	pack, err := schema.LoadProject(filepath.Join(root, "schema"))
	if err != nil {
		t.Fatalf("loading the fixture schema pack: %v", err)
	}
	return pack
}

func copyRepo(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the fixture repo: %v", err)
	}
	return dst
}

// wantErrors asserts the error-severity codes exactly, ignoring warnings.
//
// Warnings are ignored on purpose: injecting one fault often disconnects
// something else, and an orphan warning that trails a deliberate dangling
// reference says nothing about the rule under test. Warnings have their own
// cases.
func wantErrors(t *testing.T, fs world.Findings, want ...world.Code) {
	t.Helper()
	assertCodes(t, "error", fs.Filter(world.SeverityError), want)
}

// wantWarnings asserts the warning-severity codes exactly, and that the build
// would still succeed. A warning that blocks a merge is a bug: a check that is
// right most of the time must never be a gate, or the team learns to bypass
// it.
func wantWarnings(t *testing.T, fs world.Findings, want ...world.Code) {
	t.Helper()
	if fs.HasErrors() {
		t.Fatalf("warnings case produced blocking errors:\n%v", fs.Filter(world.SeverityError))
	}
	assertCodes(t, "warning", fs.Filter(world.SeverityWarning), want)
}

func assertCodes(t *testing.T, label string, got world.Findings, want []world.Code) {
	t.Helper()
	var codes []world.Code
	for _, f := range got {
		if !slices.Contains(codes, f.Code) {
			codes = append(codes, f.Code)
		}
	}
	slices.Sort(codes)
	slices.Sort(want)
	if !slices.Equal(codes, want) {
		t.Errorf("%s codes = %v, want %v\n%v", label, codes, want, got)
	}
	for _, f := range got {
		if f.File == "" || f.Line == 0 {
			t.Errorf("%s finding is unlocated, so a writer cannot act on it: %+v", label, f)
		}
	}
}

// entityFile renders a complete, valid entity file from a frontmatter body, so
// a case shows only the field it is about.
func entityFile(frontmatter string) string {
	if !strings.HasSuffix(frontmatter, "\n") {
		frontmatter += "\n"
	}
	return "---\n" + frontmatter + "---\n\nProse.\n"
}
