package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmreyer/lore-core/internal/index"
)

var fixtureRepo = filepath.Join("..", "..", "testdata", "world-ok")

// The acceptance criterion for this step, end to end: a build of a sound world
// emits both artefacts and exits zero.
func TestBuildFixtureWorld(t *testing.T) {
	out := t.TempDir()
	code, stdout, stderr := exec(t, "build", fixtureRepo, "-out", out)

	if code != exitOK {
		t.Fatalf("exit %d, want 0\nstderr:\n%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("a clean world should report nothing:\n%s", stderr)
	}
	for _, name := range []string{index.IndexFile, index.SnapshotFile} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !strings.Contains(stdout, "7 entities") || !strings.Contains(stdout, "1 statement") {
		t.Errorf("summary does not describe the world:\n%s", stdout)
	}
}

// TestBuildExitsNonZero walks the blocking-error list from the outside, which
// is where it actually matters: CI reads an exit code, not a finding list.
func TestBuildExitsNonZero(t *testing.T) {
	tests := []struct {
		name string
		file string
		body string
	}{
		{
			name: "a dangling reference",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: originates_from, target: loc_nowhere }`),
		},
		{
			name: "an unknown relation type",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: betrayed, target: char_miren }`),
		},
		{
			name: "an unknown entity type",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: deity
name: Orrin
status: canon
visibility: public`),
		},
		{
			name: "an unknown era",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
lifespan: { era: fourth_reign }`),
		},
		{
			name: "a relation violating its domain",
			file: "world/locations/vale-of-orrin.md",
			body: entity(`id: loc_vale_of_orrin
type: location
name: The Vale of Orrin
status: canon
visibility: public
relations:
  - { type: member_of, target: fac_ashen_court }`),
		},
		{
			name: "a relation violating its range",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: member_of, target: char_miren }`),
		},
		{
			name: "an authored inverse name",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: has_member, target: fac_ashen_court }`),
		},
		{
			name: "a valid_in naming an outcome that does not exist",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
valid_in: [{ decision: dec_siege_outcome, outcome: razed }]`),
		},
		{
			name: "a belief pointing at a statement that does not exist",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
beliefs:
  - { statement: stmt_nothing, value: true }`),
		},
		{
			name: "a duplicate id",
			file: "world/characters/twin.md",
			body: entity(`id: char_miren
type: character
name: Miren Again
status: canon
visibility: public`),
		},
		{
			name: "an id outside the permitted charset",
			file: "world/concepts/oathbinding.md",
			body: entity(`id: con.oathbinding
type: concept
name: Oathbinding
status: canon
visibility: public`),
		},
		{
			name: "an unknown spoiler act",
			file: "world/characters/orrin.md",
			body: entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: spoiler:act9`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := copyTree(t, fixtureRepo)
			write(t, repo, tt.file, tt.body)

			out := t.TempDir()
			code, _, stderr := exec(t, "build", repo, "-out", out)

			if code != exitInvalid {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, exitInvalid, stderr)
			}
			if !strings.Contains(stderr, "nothing written") {
				t.Errorf("stderr should say nothing was written:\n%s", stderr)
			}
			for _, name := range []string{index.IndexFile, index.SnapshotFile} {
				if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
					t.Errorf("%s exists after a failed build", name)
				}
			}
		})
	}
}

// Filenames colliding when case is ignored are exercised in internal/validate,
// in memory: Windows cannot hold both files, which is the whole reason the
// rule exists, so the case cannot be written to disk here.

// TestBuildReportsWarningsWithoutFailing: a warning that blocked a build would
// train the team to bypass the build.
func TestBuildReportsWarningsWithoutFailing(t *testing.T) {
	repo := copyTree(t, fixtureRepo)
	write(t, repo, "world/concepts/oathbinding.md", entity(`id: con_oathbinding
type: concept
name: Oathbinding
status: canon
visibility: public`))

	out := t.TempDir()
	code, stdout, stderr := exec(t, "build", repo, "-out", out)

	if code != exitOK {
		t.Fatalf("exit %d, want 0; a warning must not block a build\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "orphan") {
		t.Errorf("the warning should be reported:\n%s", stderr)
	}
	if !strings.Contains(stdout, "1 warning") {
		t.Errorf("the summary should count it:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(out, index.IndexFile)); err != nil {
		t.Errorf("the index should still be written: %v", err)
	}
}

// TestFindingsAreNavigable: a report a writer cannot open is a report they
// will not act on, so paths are printed relative to the world directory.
func TestFindingsAreNavigable(t *testing.T) {
	repo := copyTree(t, fixtureRepo)
	write(t, repo, "world/characters/orrin.md", entity(`id: char_orrin
type: character
name: Orrin
status: canon
visibility: public
relations:
  - { type: originates_from, target: loc_nowhere }`))

	_, _, stderr := exec(t, "build", repo, "-out", t.TempDir())
	if !strings.Contains(stderr, "world/characters/orrin.md:8:") {
		t.Errorf("stderr should locate the fault at a path relative to the repo:\n%s", stderr)
	}
	if !strings.Contains(stderr, "relations[0].target") {
		t.Errorf("stderr should name the field:\n%s", stderr)
	}
}

func TestBuildDefaultsToTheRepoBuildDirectory(t *testing.T) {
	repo := copyTree(t, fixtureRepo)
	if code, _, stderr := exec(t, "build", repo); code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo, index.BuildDir, index.IndexFile)); err != nil {
		t.Errorf("index not in the default build directory: %v", err)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := [][]string{
		{},
		{"frobnicate"},
		{"build"},
		{"build", "a", "b"},
	}
	for _, args := range tests {
		if code, _, _ := exec(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

// TestVersion: a writer holding a lore.exe has no other way to tell which
// release it is.
func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-version"} {
		code, stdout, stderr := exec(t, arg)
		if code != exitOK {
			t.Errorf("%s: exit %d, want 0\nstderr:\n%s", arg, code, stderr)
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(stdout), "lore "); !ok || v == "" {
			t.Errorf("%s: stdout = %q, want \"lore <version>\"", arg, stdout)
		}
	}
}

func TestMissingRepo(t *testing.T) {
	code, _, stderr := exec(t, "build", filepath.Join(t.TempDir(), "nowhere"))
	if code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "schema pack") {
		t.Errorf("the message should say what was missing:\n%s", stderr)
	}
}

// TestMissingWorldDirectory: git stores no empty directories, so a world repo
// whose writer deleted every entity has no world/ after a fresh clone. That is
// a repository that cannot be read, not a world with errors in it.
func TestMissingWorldDirectory(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
		want  []string
	}{
		{
			name: "world/ absent",
			setup: func(t *testing.T, repo string) {
				if err := os.RemoveAll(filepath.Join(repo, "world")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"world/", ".gitkeep"},
		},
		{
			name: "world is a file",
			setup: func(t *testing.T, repo string) {
				if err := os.RemoveAll(filepath.Join(repo, "world")); err != nil {
					t.Fatal(err)
				}
				write(t, repo, "world", "not a directory\n")
			},
			want: []string{"world", "not a directory"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := copyTree(t, fixtureRepo)
			tt.setup(t, repo)

			out := t.TempDir()
			code, _, stderr := exec(t, "build", repo, "-out", out)

			if code != exitUsage {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, exitUsage, stderr)
			}
			for _, s := range tt.want {
				if !strings.Contains(stderr, s) {
					t.Errorf("stderr should mention %q:\n%s", s, stderr)
				}
			}
			if strings.Contains(stderr, "unreadable") {
				t.Errorf("a missing world is not a finding:\n%s", stderr)
			}
			for _, name := range []string{index.IndexFile, index.SnapshotFile} {
				if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
					t.Errorf("%s exists after a failed build", name)
				}
			}
		})
	}
}

func exec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func entity(frontmatter string) string {
	return "---\n" + frontmatter + "\n---\n\nProse.\n"
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
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
		t.Fatalf("copying the fixture: %v", err)
	}
	return dst
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
