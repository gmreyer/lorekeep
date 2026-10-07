package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

// The Microsoft Store build of Claude Desktop reads its own copy of AppData
// under its package folder, so that copy wins over %APPDATA%.
func TestDesktopConfigCandidates(t *testing.T) {
	appdata, local, conf := t.TempDir(), t.TempDir(), t.TempDir()
	classic := filepath.Join(appdata, "Claude", DesktopConfigFile)
	store := func(pkg string) string {
		return filepath.Join(local, "Packages", pkg, "LocalCache", "Roaming", "Claude", DesktopConfigFile)
	}

	got := desktopConfigCandidates("windows", appdata, local, conf)
	if want := []string{classic}; !slices.Equal(got, want) {
		t.Errorf("no Store install: got %v, want %v", got, want)
	}

	if err := os.MkdirAll(filepath.Join(local, "Packages", "Claude_pzs8sxrjxfjjc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(local, "Packages", "ClaudeOther_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = desktopConfigCandidates("windows", appdata, local, conf)
	if want := []string{store("Claude_pzs8sxrjxfjjc")}; !slices.Equal(got, want) {
		t.Errorf("one Store install: got %v, want %v", got, want)
	}

	if err := os.MkdirAll(filepath.Join(local, "Packages", "Claude_aaaa"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = desktopConfigCandidates("windows", appdata, local, conf)
	if want := []string{store("Claude_aaaa"), store("Claude_pzs8sxrjxfjjc")}; !slices.Equal(got, want) {
		t.Errorf("two Store installs: got %v, want %v", got, want)
	}

	got = desktopConfigCandidates("darwin", "", "", conf)
	if want := []string{filepath.Join(conf, "Claude", DesktopConfigFile)}; !slices.Equal(got, want) {
		t.Errorf("not Windows: got %v, want %v", got, want)
	}
}
