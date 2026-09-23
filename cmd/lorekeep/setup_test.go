package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/project"
)

// The step's acceptance, end to end: a project set up by lorekeep builds with
// exit 0, whether it is local or ready for git, empty or with the example.
func TestSetupThenBuild(t *testing.T) {
	for _, flags := range [][]string{
		nil,
		{"-example"},
		{"-git", "-ci", "-example"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "harrowgate")
			args := append([]string{"setup", dir, "-name", "harrowgate", "-version", "v0.3.0"}, flags...)
			code, stdout, stderr := exec(t, args...)
			if code != exitOK {
				t.Fatalf("setup exit %d\nstderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, "pinned to lorekeep v0.3.0") {
				t.Errorf("setup output does not name the pin:\n%s", stdout)
			}
			if got, err := project.ReadPin(dir); err != nil || got != "v0.3.0" {
				t.Errorf("pin = %q, %v", got, err)
			}

			code, _, stderr = exec(t, "build", dir)
			if code != exitOK || stderr != "" {
				t.Fatalf("build exit %d, want 0 and no findings\nstderr:\n%s", code, stderr)
			}
		})
	}
}

func TestSetupUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no directory", []string{"setup", "-name", "w", "-version", "v0.3.0"}, "needs one directory"},
		{"no name", []string{"setup", "DIR", "-version", "v0.3.0"}, "needs one directory and -name"},
		{"ci without git", []string{"setup", "DIR", "-name", "w", "-version", "v0.3.0", "-ci"}, "git"},
		{"a bad name", []string{"setup", "DIR", "-name", "Harrow Gate", "-version", "v0.3.0"}, "project name"},
		// Tests run as a development build, so without -version there is no
		// release to pin.
		{"a development build", []string{"setup", "DIR", "-name", "w"}, "-version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "new")
			args := append([]string(nil), tt.args...)
			for i, a := range args {
				if a == "DIR" {
					args[i] = dir
				}
			}
			code, _, stderr := exec(t, args...)
			if code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr does not mention %q:\n%s", tt.want, stderr)
			}
			if _, err := os.Stat(dir); err == nil {
				t.Error("a refused setup created the directory")
			}
		})
	}
}
