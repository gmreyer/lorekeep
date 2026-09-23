package project

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func makeProject(t *testing.T, dir string) string {
	t.Helper()
	for _, sub := range []string{"schema", "world"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	abs, _ := filepath.Abs(dir)
	return abs
}

func TestIsProject(t *testing.T) {
	root := t.TempDir()
	if IsProject(root) {
		t.Error("an empty directory is a project")
	}
	if err := os.Mkdir(filepath.Join(root, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsProject(root) {
		t.Error("schema/ alone is a project")
	}
	makeProject(t, root)
	if !IsProject(root) {
		t.Error("schema/ and world/ are not a project")
	}
}

func TestRecent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cfg", "projects.json")
	if got := LoadRecent(path); len(got) != 0 {
		t.Fatalf("missing file: %v", got)
	}

	a := makeProject(t, filepath.Join(root, "a"))
	b := makeProject(t, filepath.Join(root, "b"))
	for _, d := range []string{a, b, a} {
		if err := AddRecent(path, d); err != nil {
			t.Fatal(err)
		}
	}
	if diff := cmp.Diff([]string{a, b}, LoadRecent(path)); diff != "" {
		t.Errorf("newest first, no duplicates (-want +got):\n%s", diff)
	}

	// A project that is gone drops out.
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{a}, LoadRecent(path)); diff != "" {
		t.Errorf("after removing b (-want +got):\n%s", diff)
	}

	// The list is capped.
	for i := range MaxRecent + 3 {
		if err := AddRecent(path, makeProject(t, filepath.Join(root, fmt.Sprint("p", i)))); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(LoadRecent(path)); n != MaxRecent {
		t.Errorf("%d recent projects, want %d", n, MaxRecent)
	}
}
