package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeKeepsOtherKeysAndServers(t *testing.T) {
	existing := []byte(`{"theme":"dark","mcpServers":{"other":{"command":"x","args":["1"]}}}`)
	out, changed, err := Merge(existing, "lorekeep-w", NewEntry("/bin/lk", "/w"))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var got struct {
		Theme   string           `json:"theme"`
		Servers map[string]Entry `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Theme != "dark" || got.Servers["other"].Command != "x" {
		t.Errorf("lost existing content: %s", out)
	}
	if e := got.Servers["lorekeep-w"]; e.Command != "/bin/lk" || strings.Join(e.Args, " ") != "mcp /w" {
		t.Errorf("entry = %+v", e)
	}
}

func TestMergeSameEntryUnchanged(t *testing.T) {
	out, _, _ := Merge(nil, "n", NewEntry("a", "b"))
	again, changed, err := Merge(out, "n", NewEntry("a", "b"))
	if err != nil || changed || string(again) != string(out) {
		t.Errorf("changed=%v err=%v", changed, err)
	}
	if _, changed, _ := Merge(out, "n", NewEntry("a2", "b")); !changed {
		t.Error("a different entry was not written")
	}
}

func TestMergeRejectsBrokenConfig(t *testing.T) {
	for _, in := range []string{`[1]`, `{"mcpServers":3}`, `{`} {
		if _, _, err := Merge([]byte(in), "n", NewEntry("a", "b")); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestWriteConfigBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "c.json")
	if changed, err := WriteConfig(path, "n", NewEntry("a", "b"), true); err != nil || !changed {
		t.Fatalf("create: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("a new file was backed up")
	}
	first, _ := os.ReadFile(path)
	if _, err := WriteConfig(path, "m", NewEntry("a", "b"), true); err != nil {
		t.Fatal(err)
	}
	bak, _ := os.ReadFile(path + ".bak")
	if string(bak) != string(first) {
		t.Errorf("backup = %s, want the previous file", bak)
	}
}

func TestProjectName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my_world")
	os.MkdirAll(filepath.Join(dir, "schema"), 0o755)
	if n, from := ProjectName(dir); n != "my_world" || from {
		t.Errorf("fallback = %q %v", n, from)
	}
	os.WriteFile(filepath.Join(dir, "schema", "pack.yaml"), []byte("name: eldoria\nversion: 0.1.0\n"), 0o644)
	if n, from := ProjectName(dir); n != "eldoria" || !from {
		t.Errorf("pack = %q %v", n, from)
	}
}
