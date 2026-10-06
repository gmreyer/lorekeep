package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmreyer/lorekeep/internal/mcp"
)

// installFixture is a project pinned to this (posed) version and a Claude
// Desktop config path inside a temp directory.
func installFixture(t *testing.T, interactive bool, answers string) (dir, cfg string) {
	t.Helper()
	poseAs(t, "v0.3.0")
	testSystem(t, interactive, answers)
	exe := filepath.Join(t.TempDir(), "lorekeep.exe")
	sys.executable = func() (string, error) { return exe, nil }
	dir = newProject(t, "v0.3.0")
	cfg = filepath.Join(t.TempDir(), "Claude", mcp.DesktopConfigFile)
	old := desktopConfigPath
	desktopConfigPath = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { desktopConfigPath = old })
	return dir, cfg
}

func readServers(t *testing.T, path string) (map[string]json.RawMessage, map[string]mcp.Entry) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
	var servers map[string]mcp.Entry
	if err := json.Unmarshal(top["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	return top, servers
}

func TestInstallMergesAndBacksUp(t *testing.T) {
	dir, cfg := installFixture(t, true, "y\n")
	old := `{"theme":"dark","mcpServers":{"other":{"command":"x","args":["1"]}}}`
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := exec(t, "mcp", "install", dir)
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// The project file.
	abs, _ := filepath.Abs(dir)
	_, proj := readServers(t, filepath.Join(dir, ".mcp.json"))
	e := proj["lorekeep"]
	if len(e.Args) != 2 || e.Args[0] != "mcp" || e.Args[1] != abs || e.Command == "" {
		t.Errorf(".mcp.json entry = %+v, want mcp %s", e, abs)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json.bak")); err == nil {
		t.Error("the project file was backed up; only the Desktop config is")
	}

	// The Desktop config: named after the project, everything else kept.
	top, servers := readServers(t, cfg)
	if _, ok := servers["lorekeep-w"]; !ok {
		t.Errorf("no lorekeep-w in %v", servers)
	}
	if servers["other"].Command != "x" {
		t.Error("another server was lost")
	}
	if string(top["theme"]) != `"dark"` {
		t.Error("another key was lost")
	}
	bak, err := os.ReadFile(cfg + ".bak")
	if err != nil || string(bak) != old {
		t.Errorf("backup = %q, %v; want the original file", bak, err)
	}
	if !strings.Contains(stdout, "lorekeep-w") {
		t.Errorf("the entry was not shown:\n%s", stdout)
	}
}

func TestInstallCreatesMissingConfig(t *testing.T) {
	dir, cfg := installFixture(t, true, "y\n")
	if code, _, stderr := exec(t, "mcp", "install", dir); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, s := readServers(t, cfg); len(s) != 1 {
		t.Errorf("servers = %v", s)
	}
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Error("a new config was backed up")
	}
}

func TestInstallDeclined(t *testing.T) {
	dir, cfg := installFixture(t, true, "n\n")
	if code, _, stderr := exec(t, "mcp", "install", dir); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("the Desktop config was written after a no")
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err != nil {
		t.Error("the project file was not written")
	}
}

func TestInstallIdempotent(t *testing.T) {
	dir, cfg := installFixture(t, true, "y\ny\n")
	if code, _, stderr := exec(t, "mcp", "install", dir); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	first, _ := os.ReadFile(cfg)
	proj, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))

	code, stdout, stderr := exec(t, "mcp", "install", dir)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	again, _ := os.ReadFile(cfg)
	projAgain, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if string(first) != string(again) || string(proj) != string(projAgain) {
		t.Error("a second install changed a file")
	}
	if _, s := readServers(t, cfg); len(s) != 1 {
		t.Errorf("servers = %v, want one", s)
	}
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Error("an install that changed nothing made a backup")
	}
	if !strings.Contains(stdout, "already") {
		t.Errorf("no word that it is already registered:\n%s", stdout)
	}
}

// Without a console there is nobody to ask: the entry is printed and the
// Desktop config is left alone.
func TestInstallWithoutConsoleWritesNothing(t *testing.T) {
	dir, cfg := installFixture(t, false, "y\n")
	code, stdout, stderr := exec(t, "mcp", "install", dir)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("the Desktop config was written without a console")
	}
	if _, err := os.Stat(filepath.Dir(cfg)); err == nil {
		t.Error("the Desktop config directory was created without a console")
	}
	if !strings.Contains(stdout, "lorekeep-w") || !strings.Contains(stdout, `"command"`) {
		t.Errorf("the entry was not printed:\n%s", stdout)
	}
}

func TestInstallRefusesBrokenConfig(t *testing.T) {
	dir, cfg := installFixture(t, true, "y\n")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte("{not json"), 0o644)
	if code, _, _ := exec(t, "mcp", "install", dir); code == exitOK {
		t.Error("exit 0 on a config that is not JSON")
	}
	if data, _ := os.ReadFile(cfg); string(data) != "{not json" {
		t.Error("a broken config was overwritten")
	}
}

func TestMCPUsage(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "")
	for _, args := range [][]string{
		{"mcp"},
		{"mcp", "a", "b"},
		{"mcp", "install"},
		{"mcp", "w", "-role", "admin"},
	} {
		if code, stdout, _ := exec(t, args...); code != exitUsage || stdout != "" {
			t.Errorf("%v: exit %d, stdout %q", args, code, stdout)
		}
	}
}

// A stdio server's stdout is the protocol. Whatever runPinned says, it says
// on stderr; stdout carries only the pinned release's own bytes.
func TestMCPStdoutIsProtocolOnly(t *testing.T) {
	poseAs(t, "v0.3.0")
	f := testSystem(t, false, "")
	f.publish(t, "v0.4.0")
	if err := sys.versions.InstallFrom("v0.4.0", mustExecutable(t)); err != nil {
		t.Fatal(err)
	}
	dir := newProject(t, "v0.4.0")

	code, stdout, stderr := exec(t, "mcp", dir, "-role", "author")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	if want := "fake args=mcp " + dir + " -role author nodispatch=1\n"; stdout != want {
		t.Errorf("stdout = %q, want only the child's %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}

	// A pin that is not installed: the message goes to stderr, nothing is
	// downloaded, and stdout stays empty.
	dir = newProject(t, "v0.5.0")
	code, stdout, stderr = exec(t, "mcp", dir)
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "not installed") {
		t.Errorf("missing pin: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("%d network requests without a console", n)
	}
}

// At a console an update is offered, but the offer and the answer are on
// stderr: stdout is still only the child's.
func TestMCPUpdateOfferIsOnStderr(t *testing.T) {
	poseAs(t, "v0.3.0")
	f := testSystem(t, true, "n\n")
	f.latest = "v0.5.0"
	f.notes = "new things"
	f.publish(t, "v0.4.0")
	if err := sys.versions.InstallFrom("v0.4.0", mustExecutable(t)); err != nil {
		t.Fatal(err)
	}
	dir := newProject(t, "v0.4.0")

	code, stdout, stderr := exec(t, "mcp", dir)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "fake args=mcp ") || strings.Count(stdout, "\n") != 1 {
		t.Errorf("stdout = %q, want only the child's line", stdout)
	}
	if !strings.Contains(stderr, "v0.5.0 is available") {
		t.Errorf("the update offer is not on stderr: %q", stderr)
	}
}
