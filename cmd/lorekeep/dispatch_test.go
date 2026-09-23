package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmreyer/lorekeep/internal/project"
	"github.com/gmreyer/lorekeep/internal/scaffold"
	"github.com/gmreyer/lorekeep/internal/versions"
)

// The test binary doubles as every downloaded lorekeep.exe: run with fakeEnv
// set, it prints its arguments and whether dispatch was disabled, and exits
// with the code in fakeExitEnv.
const (
	fakeEnv     = "LOREKEEP_TEST_FAKE"
	fakeExitEnv = "LOREKEEP_TEST_FAKE_EXIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) != "" {
		fmt.Printf("fake args=%s nodispatch=%s\n", strings.Join(os.Args[1:], " "), os.Getenv(versions.NoDispatchEnv))
		code, _ := strconv.Atoi(os.Getenv(fakeExitEnv))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// releases is a fake of GitHub: release assets and the latest-release API.
type releases struct {
	assets   map[string][]byte
	latest   string
	notes    string
	requests atomic.Int32
}

func (f *releases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if r.URL.Path == "/api/releases/latest" {
		if f.latest == "" {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"tag_name": f.latest, "body": f.notes})
		return
	}
	body, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(body)
}

// publish makes tag downloadable, as a copy of this test binary.
func (f *releases) publish(t *testing.T, tag string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	f.assets[tag+"/"+versions.Asset] = body
	f.assets[tag+"/SHA256SUMS"] = []byte(hex.EncodeToString(sum[:]) + "  " + versions.Asset + "\n")
}

// testSystem replaces sys for one test. answers is what the writer types, one
// line per prompt; interactive says whether there is a terminal at all.
func testSystem(t *testing.T, interactive bool, answers string) *releases {
	t.Helper()
	f := &releases{assets: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	old := sys
	sys = &system{
		in:          bufio.NewReader(strings.NewReader(answers)),
		interactive: interactive,
		versions: &versions.Manager{
			Dir:       filepath.Join(t.TempDir(), "versions"),
			Downloads: srv.URL + "/dl",
			API:       srv.URL + "/api",
			HTTP:      srv.Client(),
		},
		statePath:  filepath.Join(t.TempDir(), "state.json"),
		now:        func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
		executable: os.Executable,
	}
	t.Cleanup(func() { sys = old })
	t.Setenv(fakeEnv, "1") // only children read it; this process is past TestMain
	t.Setenv(fakeExitEnv, "0")
	return f
}

// newProject sets up a real, clean project pinned to pin.
func newProject(t *testing.T, pin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "world")
	if _, err := scaffold.Setup(dir, scaffold.Options{Name: "w", Version: pin, Example: true}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pinOf(t *testing.T, dir string) string {
	t.Helper()
	v, err := project.ReadPin(dir)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A build runs the pinned version when it is not this binary: same
// arguments, its exit code, and the child told not to dispatch again.
func TestBuildRunsPinnedVersion(t *testing.T) {
	poseAs(t, "v0.3.0")
	f := testSystem(t, false, "")
	f.publish(t, "v0.4.0")
	if err := sys.versions.InstallFrom("v0.4.0", mustExecutable(t)); err != nil {
		t.Fatal(err)
	}
	dir := newProject(t, "v0.4.0")

	for _, want := range []int{exitOK, exitInvalid} {
		t.Setenv(fakeExitEnv, strconv.Itoa(want))
		code, stdout, stderr := exec(t, "build", dir, "-out", "dist")
		if code != want {
			t.Errorf("exit %d, want %d\nstderr:\n%s", code, want, stderr)
		}
		if w := "fake args=build " + dir + " -out dist nodispatch=1"; !strings.Contains(stdout, w) {
			t.Errorf("child did not run as %q:\n%s", w, stdout)
		}
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("%d network requests without a terminal, want 0", n)
	}
}

// Without a terminal there is nobody to ask, so a missing version is an
// error and nothing is downloaded.
func TestBuildMissingVersionWithoutTerminal(t *testing.T) {
	poseAs(t, "v0.3.0")
	f := testSystem(t, false, "")
	f.publish(t, "v0.4.0")
	dir := newProject(t, "v0.4.0")

	code, _, stderr := exec(t, "build", dir)
	if code != exitUsage || !strings.Contains(stderr, "v0.4.0, which is not installed") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if sys.versions.Installed("v0.4.0") || f.requests.Load() != 0 {
		t.Error("downloaded without being asked")
	}
}

func TestBuildMissingVersionAsks(t *testing.T) {
	for _, tt := range []struct {
		answer   string
		wantCode int
		wantRun  bool
	}{
		{"\n", exitOK, true}, // the default is yes
		{"y\n", exitOK, true},
		{"n\n", exitUsage, false},
	} {
		t.Run(strconv.Quote(tt.answer), func(t *testing.T) {
			poseAs(t, "v0.3.0")
			f := testSystem(t, true, tt.answer)
			f.publish(t, "v0.4.0")
			dir := newProject(t, "v0.4.0")

			code, stdout, stderr := exec(t, "build", dir)
			if code != tt.wantCode {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, "not installed. Download it? [Y/n]") {
				t.Errorf("no download prompt:\n%s", stderr)
			}
			if ran := strings.Contains(stdout, "fake args=build"); ran != tt.wantRun {
				t.Errorf("pinned version ran: %v, want %v", ran, tt.wantRun)
			}
			if sys.versions.Installed("v0.4.0") != tt.wantRun {
				t.Errorf("installed: %v, want %v", !tt.wantRun, tt.wantRun)
			}
		})
	}
}

func TestBuildWhenPinIsThisVersion(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "")
	dir := newProject(t, "v0.3.0")
	code, stdout, stderr := exec(t, "build", dir)
	if code != exitOK || stderr != "" || strings.Contains(stdout, "fake") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestBuildNoDispatchEnvRunsInProcess(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "")
	t.Setenv(versions.NoDispatchEnv, "1")
	dir := newProject(t, "v0.9.0")
	code, stdout, stderr := exec(t, "build", dir)
	if code != exitOK || stderr != "" || strings.Contains(stdout, "fake") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestBuildDevelopmentIgnoresPin(t *testing.T) {
	poseAs(t, "(devel)")
	testSystem(t, false, "")
	dir := newProject(t, "v0.9.0")
	code, _, stderr := exec(t, "build", dir)
	if code != exitOK || !strings.Contains(stderr, "development build (devel); ignoring the project's pin v0.9.0") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
}

func TestBuildRefusesBadPins(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "")
	for pin, want := range map[string]string{
		"v0.2.1\n": "first lorekeep that reads it is v0.3.0",
		"latest\n": "want one line like v1.2.3",
	} {
		dir := newProject(t, "v0.3.0")
		if err := os.WriteFile(filepath.Join(dir, project.PinFile), []byte(pin), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := exec(t, "build", dir)
		if code != exitUsage || !strings.Contains(stderr, want) {
			t.Errorf("pin %q: exit %d, stderr:\n%s", pin, code, stderr)
		}
	}
}

// The offer names the release and shows its notes; no leaves the pin alone
// and is remembered, so the same release is not offered twice.
func TestBuildOffersUpdateOnce(t *testing.T) {
	poseAs(t, "v0.3.0")
	f := testSystem(t, true, "n\n")
	f.latest, f.notes = "v0.4.0", "Adds the thing."
	f.publish(t, "v0.4.0")
	dir := newProject(t, "v0.3.0")

	code, _, stderr := exec(t, "build", dir)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	for _, want := range []string{"lorekeep v0.4.0 is available; this project uses v0.3.0.", "Adds the thing.", "[y/N]"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("offer lacks %q:\n%s", want, stderr)
		}
	}
	if pinOf(t, dir) != "v0.3.0" {
		t.Error("declining moved the pin")
	}

	// A day later the check runs again, but a declined release stays quiet.
	sys.now = func() time.Time { return time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC) }
	_, _, stderr = exec(t, "build", dir)
	if strings.Contains(stderr, "available") {
		t.Errorf("offered a declined release again:\n%s", stderr)
	}
}

// Yes moves the pin once the new version builds the project, and the build
// then runs with it.
func TestBuildAcceptsUpdate(t *testing.T) {
	poseAs(t, "v0.3.0")
	f := testSystem(t, true, "y\n")
	f.latest = "v0.4.0"
	f.publish(t, "v0.4.0")
	dir := newProject(t, "v0.3.0")

	code, stdout, stderr := exec(t, "build", dir)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	if pinOf(t, dir) != "v0.4.0" {
		t.Errorf("pin = %s, want v0.4.0", pinOf(t, dir))
	}
	if !strings.Contains(stdout, "pinned to lorekeep v0.4.0 (was v0.3.0)") ||
		strings.Count(stdout, "fake args=build") != 2 { // the check, then the build
		t.Errorf("stdout:\n%s", stdout)
	}
}

// Offline, a build neither asks nor fails.
func TestBuildOfflineIsQuiet(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, true, "")
	dir := newProject(t, "v0.3.0")
	code, _, stderr := exec(t, "build", dir)
	if code != exitOK || stderr != "" {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
}

func TestUpdate(t *testing.T) {
	tests := []struct {
		name      string
		pin       string
		latest    string
		args      []string
		childExit int
		wantCode  int
		wantPin   string
		wantOut   string
	}{
		{"to the latest", "v0.3.0", "v0.4.0", nil, 0, exitOK, "v0.4.0", "pinned to lorekeep v0.4.0 (was v0.3.0)"},
		{"to a named version", "v0.3.0", "v0.5.0", []string{"-version", "v0.4.0"}, 0, exitOK, "v0.4.0", "pinned to lorekeep v0.4.0"},
		{"down on request", "v0.4.0", "v0.4.0", []string{"-version", "v0.3.0"}, 0, exitOK, "v0.3.0", "pinned to lorekeep v0.3.0 (was v0.4.0)"},
		{"not down by default", "v0.5.0", "v0.4.0", nil, 0, exitOK, "v0.5.0", "already on lorekeep v0.5.0"},
		{"already latest", "v0.4.0", "v0.4.0", nil, 0, exitOK, "v0.4.0", "already on lorekeep v0.4.0"},
		{"new version finds errors", "v0.3.0", "v0.4.0", nil, exitInvalid, exitInvalid, "v0.3.0", "fake args=build"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			poseAs(t, "v0.3.0")
			f := testSystem(t, false, "")
			f.latest = tt.latest
			for _, v := range []string{"v0.4.0", "v0.5.0"} {
				f.publish(t, v)
			}
			t.Setenv(fakeExitEnv, strconv.Itoa(tt.childExit))
			dir := newProject(t, tt.pin)

			code, stdout, stderr := exec(t, append([]string{"update", dir}, tt.args...)...)
			if code != tt.wantCode {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, tt.wantCode, stderr)
			}
			if got := pinOf(t, dir); got != tt.wantPin {
				t.Errorf("pin = %s, want %s", got, tt.wantPin)
			}
			if !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantOut, stdout)
			}
		})
	}
}

func TestUpdateErrors(t *testing.T) {
	poseAs(t, "v0.3.0")
	testSystem(t, false, "") // offline: no latest release
	dir := newProject(t, "v0.3.0")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"update", dir}, "cannot find the latest release"},
		{[]string{"update", dir, "-version", "v0.2.1"}, "first lorekeep that reads"},
		{[]string{"update", dir, "-version", "main"}, "not a release version"},
		{[]string{"update", dir, "-version", "v0.9.0"}, "no such release"},
		{[]string{"update", t.TempDir()}, "no lorekeep-version file"},
	} {
		code, _, stderr := exec(t, tt.args...)
		if code != exitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, stderr:\n%s", tt.args[2:], code, stderr)
		}
	}
	if pinOf(t, dir) != "v0.3.0" {
		t.Error("a failed update moved the pin")
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}
