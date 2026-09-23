package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitCommand(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "")
	dir := newProject(t, "v0.3.0")

	code, stdout, stderr := exec(t, "git", dir, "-ci")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	for _, want := range []string{"wrote .gitattributes", "wrote .github/workflows/build.yml", "no secrets"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	code, stdout, _ = exec(t, "git", dir, "-ci")
	if code != exitOK || strings.Contains(stdout, "wrote") || !strings.Contains(stdout, "already present .gitignore") {
		t.Errorf("second run: exit %d\n%s", code, stdout)
	}

	// The project still builds: nothing git adds is read as lore.
	if code, _, stderr := exec(t, "build", dir); code != exitOK || stderr != "" {
		t.Errorf("build after git: exit %d\n%s", code, stderr)
	}
}

func TestGitCommandAsksBeforeReplacing(t *testing.T) {
	for _, tt := range []struct {
		name        string
		interactive bool
		answer      string
		replaced    bool
	}{
		{"no terminal keeps it", false, "", false},
		{"no keeps it", true, "n\n", false},
		{"enter keeps it", true, "\n", false},
		{"yes replaces it", true, "y\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			poseAs(t, "v0.3.0")
			testSystem(t, tt.interactive, tt.answer)
			dir := newProject(t, "v0.3.0")
			path := filepath.Join(dir, ".gitignore")
			if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := exec(t, "git", dir)
			if code != exitOK {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
			got, _ := os.ReadFile(path)
			if replaced := string(got) != "mine\n"; replaced != tt.replaced {
				t.Errorf("replaced = %v, want %v\nstdout:\n%s", replaced, tt.replaced, stdout)
			}
			if tt.interactive && !strings.Contains(stderr, ".gitignore exists and differs") {
				t.Errorf("not asked:\n%s", stderr)
			}
		})
	}
}

func TestGitCommandNeedsAProject(t *testing.T) {
	testSystem(t, false, "")
	code, _, stderr := exec(t, "git", t.TempDir())
	if code != exitUsage || !strings.Contains(stderr, "not a lorekeep project") {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

func TestGitCommandRunsPinnedVersion(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "")
	if err := sys.versions.InstallFrom("v0.4.0", mustExecutable(t)); err != nil {
		t.Fatal(err)
	}
	dir := newProject(t, "v0.4.0")
	code, stdout, _ := exec(t, "git", dir, "-ci")
	if code != exitOK || !strings.Contains(stdout, "fake args=git "+dir+" -ci") {
		t.Errorf("exit %d\n%s", code, stdout)
	}
}
