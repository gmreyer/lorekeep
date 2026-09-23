package scaffold

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func localProject(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "w")
	if _, err := Setup(dir, Options{Name: "w", Version: testVersion, Example: true}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A local project gains exactly what setup -git (-ci) would have written.
func TestAddGitMatchesSetup(t *testing.T) {
	for _, ci := range []bool{false, true} {
		dir := localProject(t)
		res, err := AddGit(dir, ci, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := gitOnly
		if ci {
			want = concat(gitOnly, ciOnly)
		}
		if diff := cmp.Diff(sorted(want), res.Written); diff != "" {
			t.Errorf("ci=%v written (-want +got):\n%s", ci, diff)
		}

		fresh := filepath.Join(t.TempDir(), "fresh")
		if _, err := Setup(fresh, Options{Name: "w", Version: testVersion, Example: true, Git: true, CI: ci}); err != nil {
			t.Fatal(err)
		}
		for _, p := range want {
			if a, b := read(t, filepath.Join(dir, p)), read(t, filepath.Join(fresh, p)); a != b {
				t.Errorf("%s differs from what setup writes", p)
			}
		}
	}
}

// Running it twice changes nothing the second time.
func TestAddGitIsIdempotent(t *testing.T) {
	dir := localProject(t)
	if _, err := AddGit(dir, true, nil); err != nil {
		t.Fatal(err)
	}
	res, err := AddGit(dir, true, func(string) bool { t.Error("asked about an identical file"); return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || len(res.Kept) != 0 {
		t.Errorf("second run: %+v", res)
	}
	if diff := cmp.Diff(sorted(concat(gitOnly, ciOnly)), res.Present); diff != "" {
		t.Errorf("present (-want +got):\n%s", diff)
	}
}

// A writer's own file is never replaced unless they say so, and they are
// asked about that file only.
func TestAddGitKeepsWritersFiles(t *testing.T) {
	for _, agree := range []bool{false, true} {
		dir := localProject(t)
		mine := "# mine\nnotes/\n"
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(mine), 0o644); err != nil {
			t.Fatal(err)
		}
		var asked []string
		res, err := AddGit(dir, false, func(p string) bool { asked = append(asked, p); return agree })
		if err != nil {
			t.Fatal(err)
		}
		if !cmp.Equal(asked, []string{".gitignore"}) {
			t.Errorf("asked about %v, want only .gitignore", asked)
		}
		got := read(t, filepath.Join(dir, ".gitignore"))
		if agree {
			if got == mine || !slices.Contains(res.Written, ".gitignore") {
				t.Errorf("agreed, but .gitignore was not replaced: %+v", res)
			}
		} else {
			if got != mine || !cmp.Equal(res.Kept, []string{".gitignore"}) {
				t.Errorf("declined, but .gitignore changed: %+v", res)
			}
		}
	}
}

func TestAddGitNeedsAProject(t *testing.T) {
	dir := t.TempDir()
	_, err := AddGit(dir, false, nil)
	if err == nil || !strings.Contains(err.Error(), "not a lorekeep project") {
		t.Fatalf("err = %v", err)
	}
	if got := onDisk(t, dir); len(got) != 0 {
		t.Errorf("wrote into a non-project: %v", got)
	}
}
