package scaffold

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/project"
	"github.com/gmreyer/lorekeep/internal/schema"
)

const testVersion = "v0.3.0"

var (
	baseFiles = []string{
		"README.md",
		"lorekeep-version",
		"schema/acts.yaml",
		"schema/entity-types.yaml",
		"schema/eras.yaml",
		"schema/pack.yaml",
		"schema/relations.yaml",
	}
	exampleFiles = []string{
		"world/characters/ilse-marrow.md",
		"world/characters/tomas-marrow.md",
		"world/events/harrowgate-fire.md",
		"world/factions/lantern-guild.md",
		"world/locations/harrowgate.md",
	}
	gitOnly = []string{".gitattributes", ".gitignore", "world/.gitkeep"}
	ciOnly  = []string{".github/workflows/build.yml"}
)

// Every variant is a project the build accepts cleanly: exit 0 and no
// findings at all, because a writer's first build must not start with noise.
func TestSetupVariantsBuildClean(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{"local", Options{}, baseFiles},
		{"local with example", Options{Example: true}, concat(baseFiles, exampleFiles)},
		{"git", Options{Git: true}, concat(baseFiles, gitOnly)},
		{"git and ci", Options{Git: true, CI: true}, concat(baseFiles, gitOnly, ciOnly)},
		{"everything", Options{Example: true, Git: true, CI: true}, concat(baseFiles, exampleFiles, gitOnly, ciOnly)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "myworld")
			o := tt.opts
			o.Name, o.Version = "myworld", testVersion

			written, err := Setup(dir, o)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(sorted(tt.want), written); diff != "" {
				t.Errorf("written files (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(sorted(tt.want), onDisk(t, dir)); diff != "" {
				t.Errorf("files on disk (-want +got):\n%s", diff)
			}
			if info, err := os.Stat(filepath.Join(dir, index.WorldDir)); err != nil || !info.IsDir() {
				t.Fatalf("world/ missing: %v", err)
			}

			res, err := index.Build(dir, filepath.Join(dir, index.BuildDir))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			for _, f := range res.Findings {
				t.Errorf("finding: %s", f)
			}
		})
	}
}

// A local project carries nothing that only git reads.
func TestSetupLocalHasNoGitFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir, Options{Name: "w", Version: testVersion, Example: true}); err != nil {
		t.Fatal(err)
	}
	for _, p := range concat(gitOnly, ciOnly) {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
			t.Errorf("%s written without the git option", p)
		}
	}
}

func TestSetupContents(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir, Options{Name: "harrowgate", Version: testVersion}); err != nil {
		t.Fatal(err)
	}

	if got, err := project.ReadPin(dir); err != nil || got != testVersion {
		t.Errorf("pin = %q, %v; want %s", got, err, testVersion)
	}

	pack, err := schema.LoadProject(filepath.Join(dir, index.SchemaDir))
	if err != nil {
		t.Fatal(err)
	}
	core, err := schema.CoreVersion()
	if err != nil {
		t.Fatal(err)
	}
	if pack.Name != "harrowgate" {
		t.Errorf("pack name = %q, want harrowgate", pack.Name)
	}
	if pack.CoreVersion != core {
		t.Errorf("core_version = %q, want the embedded core %q", pack.CoreVersion, core)
	}

	readme := read(t, filepath.Join(dir, "README.md"))
	if !strings.HasPrefix(readme, "# harrowgate\n") {
		t.Errorf("README does not open with the project name:\n%.80s", readme)
	}
}

// Files are written with LF endings whatever the platform, since the
// .gitattributes they may sit beside says so.
func TestSetupWritesLF(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir, Options{Name: "w", Version: testVersion, Example: true, Git: true, CI: true}); err != nil {
		t.Fatal(err)
	}
	for _, p := range onDisk(t, dir) {
		if strings.Contains(read(t, filepath.Join(dir, filepath.FromSlash(p))), "\r") {
			t.Errorf("%s contains CR", p)
		}
	}
}

func TestSetupAcceptsExistingEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir, Options{Name: "w", Version: testVersion}); err != nil {
		t.Fatal(err)
	}
}

func TestSetupRefuses(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"an uppercase name", Options{Name: "Harrowgate", Version: testVersion}, "project name"},
		{"a name with a hyphen", Options{Name: "harrow-gate", Version: testVersion}, "project name"},
		{"an empty name", Options{Version: testVersion}, "project name"},
		{"a devel version", Options{Name: "w", Version: "(devel)"}, "version"},
		{"a pre-release version", Options{Name: "w", Version: "v0.3.0-rc.1"}, "version"},
		{"a version before pins", Options{Name: "w", Version: "v0.2.1"}, "first version"},
		{"ci without git", Options{Name: "w", Version: testVersion, CI: true}, "git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "new")
			_, err := Setup(dir, tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tt.want)
			}
			if _, err := os.Stat(dir); err == nil {
				t.Error("a refused setup created the directory")
			}
		})
	}
}

func TestSetupRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(mine, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(dir, Options{Name: "w", Version: testVersion}); err == nil {
		t.Fatal("setup into a non-empty directory succeeded")
	}
	if got := onDisk(t, dir); !cmp.Equal(got, []string{"notes.txt"}) {
		t.Errorf("directory changed: %v", got)
	}
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// onDisk lists the files under dir, slash-separated, skipping build output.
func onDisk(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == index.BuildDir {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sorted(out)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
