package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Nobody can answer a prompt on the null device or a file, and CI runs with
// stdin on one of them.
func TestIsTerminalRefusesNullAndFiles(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Errorf("%s counts as a terminal", os.DevNull)
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a regular file counts as a terminal")
	}
}
