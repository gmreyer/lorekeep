package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/project"
)

// menuRun starts lorekeep with no arguments at a console, typing input.
func menuRun(t *testing.T, input string) (int, string, string) {
	t.Helper()
	sys.in.Reset(strings.NewReader(input))
	return exec(t)
}

func TestNoArgumentsWithoutConsolePrintsUsage(t *testing.T) {
	testSystem(t, false, "")
	code, _, stderr := exec(t)
	if code != exitUsage || !strings.Contains(stderr, "usage:") {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

func TestMenuWithoutProject(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	code, stdout, _ := menuRun(t, "7\nq\n")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"lorekeep v0.3.0", "No project open.", "1) Set up a new project", "2) Open a project", "q) Quit", `No item "7".`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("menu lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Build") {
		t.Errorf("Build offered with no project:\n%s", stdout)
	}
}

// Input ending — a closed console — quits rather than spinning.
func TestMenuQuitsAtEndOfInput(t *testing.T) {
	testSystem(t, true, "")
	if code, _, _ := menuRun(t, ""); code != exitOK {
		t.Errorf("exit %d", code)
	}
	if code, _, _ := menuRun(t, "1\n"); code != exitOK { // mid-dialog
		t.Errorf("exit %d", code)
	}
}

// Setting up through the menu asks every question, runs setup, and opens the
// new project.
func TestMenuSetup(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	dir := filepath.Join(t.TempDir(), "Harrow Gate")

	// Set up; folder; accept the suggested name; example, git, CI; Enter; quit.
	code, stdout, stderr := menuRun(t, "1\n\""+dir+"\"\n\ny\ny\ny\n\nq\n")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	for _, want := range []string{"Project name [harrow_gate]:", "set up harrow_gate", "Project: " + dir + " (lorekeep v0.3.0)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	for _, p := range []string{"world/characters/ilse-marrow.md", ".gitattributes", ".github/workflows/build.yml"} {
		if !exists(filepath.Join(dir, filepath.FromSlash(p))) {
			t.Errorf("%s not written", p)
		}
	}
	if got := project.LoadRecent(sys.recentPath); len(got) != 1 || got[0] != dir {
		t.Errorf("recent = %v", got)
	}
}

func TestMenuSetupLocalByDefault(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	dir := filepath.Join(t.TempDir(), "w")
	// Enter to every question takes the defaults: no example, no git.
	if code, _, stderr := menuRun(t, "1\n"+dir+"\n\n\n\n\nq\n"); code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if exists(filepath.Join(dir, ".gitattributes")) || exists(filepath.Join(dir, "world", "characters")) {
		t.Error("defaults added git files or the example")
	}
	if !exists(filepath.Join(dir, project.PinFile)) {
		t.Error("no project was set up")
	}
}

// The menu opens on the last project, builds it, and moves on from "Prepare
// for git" to "Add a GitHub CI workflow" once the git files are there.
func TestMenuProjectItems(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	dir := newProject(t, "v0.3.0")
	remember(dir)

	// Build, Enter; Prepare for git without CI, Enter; quit.
	code, stdout, stderr := menuRun(t, "1\n\n3\nn\n\nq\n")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	for _, want := range []string{
		"Project: " + dir,
		"1) Build", "2) Update lorekeep for this project", "3) Prepare for git",
		"built w 0.1.0",
		"wrote .gitattributes",
		"3) Add a GitHub CI workflow",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if exists(filepath.Join(dir, ".github")) {
		t.Error("CI added although declined")
	}
}

func TestMenuOpen(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	a, b := newProject(t, "v0.3.0"), newProject(t, "v0.3.0")
	remember(a)
	remember(b) // b is now first, and the menu opens on it

	// Open another project: 2 in the recent list is a; then a non-project.
	code, stdout, _ := menuRun(t, "4\n2\n4\n"+t.TempDir()+"\n\nq\n")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "Project: "+b) || !strings.Contains(stdout, "Project: "+a) {
		t.Errorf("did not move from b to a:\n%s", stdout)
	}
	if !strings.Contains(stdout, "is not a lorekeep project") {
		t.Errorf("a non-project was not refused:\n%s", stdout)
	}
}

// Started from inside a project, the menu opens that one.
func TestMenuOpensCurrentDirectory(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	remember(newProject(t, "v0.3.0"))
	here := newProject(t, "v0.3.0")
	t.Chdir(here)

	_, stdout, _ := menuRun(t, "q\n")
	if !strings.Contains(stdout, "Project: "+here) {
		t.Errorf("did not open the current directory:\n%s", stdout)
	}
}

func TestSuggestName(t *testing.T) {
	for in, want := range map[string]string{
		"work/Harrow Gate": "harrow_gate",
		"harrowgate":       "harrowgate",
		"Lore--World 2":    "lore_world_2",
		"Ärger":            "rger",
		"---":              "world",
	} {
		if got := suggestName(filepath.FromSlash(in)); got != want {
			t.Errorf("suggestName(%q) = %q, want %q", in, got, want)
		}
	}
}
