package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePin(t *testing.T) {
	valid := map[string]string{
		"v0.3.0":     "v0.3.0",
		"v0.3.0\n":   "v0.3.0",
		"v0.3.0\r\n": "v0.3.0",
		"v10.20.300": "v10.20.300",
		"v1.0.0\n":   "v1.0.0",
	}
	for in, want := range valid {
		got, err := ParsePin(in)
		if err != nil || got != want {
			t.Errorf("ParsePin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	invalid := []string{
		"",
		"\n",
		"0.3.0",
		"v0.3",
		"v0.3.0-rc.1",
		"v0.3.1-0.20260923075144-269ebc5ca44d",
		"v01.2.3",
		" v0.3.0",
		"v0.3.0 ",
		"v0.3.0\n\n",
		"v0.3.0\nv0.4.0",
		"(devel)",
	}
	for _, in := range invalid {
		if got, err := ParsePin(in); err == nil {
			t.Errorf("ParsePin(%q) = %q, want an error", in, got)
		}
	}
}

func TestPinRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadPin(dir); !errors.Is(err, ErrNoPin) {
		t.Fatalf("ReadPin on an empty dir: %v, want ErrNoPin", err)
	}
	if err := WritePin(dir, "v0.3.0"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, PinFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v0.3.0\n" {
		t.Errorf("pin file = %q, want %q", data, "v0.3.0\n")
	}
	if got, err := ReadPin(dir); err != nil || got != "v0.3.0" {
		t.Errorf("ReadPin = %q, %v", got, err)
	}
	if err := WritePin(dir, "(devel)"); err == nil {
		t.Error("WritePin accepted a non-release version")
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v0.3.0", "v0.3.0", 0},
		{"v0.3.0", "v0.3.1", -1},
		{"v0.10.0", "v0.9.9", 1},
		{"v1.0.0", "v0.99.99", 1},
		{"v0.2.1", "v0.3.0", -1},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
