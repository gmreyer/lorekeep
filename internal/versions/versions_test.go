package versions

import (
	"bytes"
	"context"
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
)

// The test binary doubles as a fake lorekeep.exe: run with fakeEnv set, it
// prints its arguments and whether dispatch was disabled, and exits with the
// code in fakeExitEnv.
const (
	fakeEnv     = "LOREKEEP_TEST_FAKE"
	fakeExitEnv = "LOREKEEP_TEST_FAKE_EXIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) != "" {
		fmt.Printf("args=%s nodispatch=%s\n", strings.Join(os.Args[1:], " "), os.Getenv(NoDispatchEnv))
		code, _ := strconv.Atoi(os.Getenv(fakeExitEnv))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// fakeReleases serves release assets and the latest-release endpoint, and
// counts requests so a test can tell whether the network was asked.
type fakeReleases struct {
	assets   map[string][]byte // "<tag>/<asset>" → body
	latest   string
	notes    string
	requests atomic.Int32
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if r.URL.Path == "/api/releases/latest" {
		if f.latest == "" {
			http.Error(w, "down", http.StatusServiceUnavailable)
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

// publish adds a release whose SHA256SUMS matches exe, or lists sum instead
// when sum is not empty.
func (f *fakeReleases) publish(tag string, exe []byte, sum string) {
	if sum == "" {
		h := sha256.Sum256(exe)
		sum = hex.EncodeToString(h[:])
	}
	f.assets[tag+"/"+Asset] = exe
	f.assets[tag+"/"+sumsAsset] = []byte(sum + "  " + Asset + "\n")
}

func newTest(t *testing.T) (*Manager, *fakeReleases) {
	t.Helper()
	f := &fakeReleases{assets: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Manager{
		Dir:       filepath.Join(t.TempDir(), "versions"),
		Downloads: srv.URL + "/dl",
		API:       srv.URL + "/api",
		HTTP:      srv.Client(),
	}, f
}

func selfBytes(t *testing.T) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstall(t *testing.T) {
	m, f := newTest(t)
	exe := []byte("pretend binary")
	f.publish("v0.3.0", exe, "")

	if m.Installed("v0.3.0") {
		t.Fatal("installed before Install")
	}
	if err := m.Install(context.Background(), "v0.3.0"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(m.Path("v0.3.0"))
	if err != nil || !bytes.Equal(got, exe) {
		t.Fatalf("installed %q, %v; want %q", got, err, exe)
	}

	// Installing again touches nothing, network included.
	before := f.requests.Load()
	if err := m.Install(context.Background(), "v0.3.0"); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != before {
		t.Error("a second Install went to the network")
	}
}

// A tampered download is refused and leaves the cache as it was: no binary,
// no partial file.
func TestInstallRejectsHashMismatch(t *testing.T) {
	m, f := newTest(t)
	f.publish("v0.3.0", []byte("tampered"), strings.Repeat("ab", 32))

	err := m.Install(context.Background(), "v0.3.0")
	if err == nil || !strings.Contains(err.Error(), "does not match SHA256SUMS") {
		t.Fatalf("err = %v, want a hash mismatch", err)
	}
	if m.Installed("v0.3.0") {
		t.Error("a tampered binary was installed")
	}
	leftovers, _ := os.ReadDir(filepath.Dir(m.Path("v0.3.0")))
	for _, e := range leftovers {
		t.Errorf("left behind: %s", e.Name())
	}
}

func TestInstallErrors(t *testing.T) {
	m, f := newTest(t)
	f.assets["v0.4.0/"+sumsAsset] = []byte("abc  something-else.exe\n")
	f.assets["v0.4.0/"+Asset] = []byte("x")

	tests := []struct{ version, want string }{
		{"v9.9.9", "no such release"},
		{"v0.4.0", "does not list lorekeep.exe"},
		{"(devel)", "not a release version"},
	}
	for _, tt := range tests {
		err := m.Install(context.Background(), tt.version)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Install(%s) = %v, want %q", tt.version, err, tt.want)
		}
		if tt.version != "(devel)" && m.Installed(tt.version) {
			t.Errorf("%s installed after a failure", tt.version)
		}
	}
}

func TestInstallFrom(t *testing.T) {
	m, _ := newTest(t)
	src := filepath.Join(t.TempDir(), "lorekeep.exe")
	if err := os.WriteFile(src, []byte("me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallFrom("v0.3.0", src); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(m.Path("v0.3.0")); string(got) != "me" {
		t.Errorf("installed %q", got)
	}
}

// Run passes arguments and the exit code through, and marks the child so it
// never dispatches again.
func TestRun(t *testing.T) {
	m, f := newTest(t)
	f.publish("v0.3.0", selfBytes(t), "")
	if err := m.Install(context.Background(), "v0.3.0"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeEnv, "1")

	for _, code := range []int{0, 1, 2} {
		t.Setenv(fakeExitEnv, strconv.Itoa(code))
		var out bytes.Buffer
		got, err := m.Run("v0.3.0", []string{"build", "my world"}, nil, &out, &out)
		if err != nil {
			t.Fatal(err)
		}
		if got != code {
			t.Errorf("exit %d, want %d", got, code)
		}
		if want := "args=build my world nodispatch=1\n"; out.String() != want {
			t.Errorf("child printed %q, want %q", out.String(), want)
		}
	}
}

func TestLatest(t *testing.T) {
	m, f := newTest(t)
	f.latest, f.notes = "v0.4.0", "Adds things."
	r, err := m.Latest(context.Background())
	if err != nil || r != (Release{Tag: "v0.4.0", Notes: "Adds things."}) {
		t.Fatalf("Latest = %+v, %v", r, err)
	}

	f.latest = "nightly"
	if _, err := m.Latest(context.Background()); err == nil {
		t.Error("a non-release tag was accepted")
	}
}

// Refresh asks at most once per interval, and a failed request neither
// errors nor forgets the last release it knew.
func TestRefresh(t *testing.T) {
	m, f := newTest(t)
	f.latest = "v0.4.0"
	var s State
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	if r := m.Refresh(context.Background(), &s, now); r == nil || r.Tag != "v0.4.0" {
		t.Fatalf("first refresh = %+v", r)
	}
	f.latest = "v0.5.0"
	if r := m.Refresh(context.Background(), &s, now.Add(23*time.Hour)); r.Tag != "v0.4.0" {
		t.Errorf("refreshed within the interval: %+v", r)
	}
	if n := f.requests.Load(); n != 1 {
		t.Errorf("%d requests within the interval, want 1", n)
	}
	if r := m.Refresh(context.Background(), &s, now.Add(25*time.Hour)); r.Tag != "v0.5.0" {
		t.Errorf("did not refresh after the interval: %+v", r)
	}

	f.latest = "" // offline
	if r := m.Refresh(context.Background(), &s, now.Add(50*time.Hour)); r == nil || r.Tag != "v0.5.0" {
		t.Errorf("offline refresh lost the last release: %+v", r)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lorekeep", "state.json")
	if s := LoadState(path); s.LastCheck != (time.Time{}) || s.Latest != nil {
		t.Errorf("missing file loaded as %+v", s)
	}

	s := State{LastCheck: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC), Latest: &Release{Tag: "v0.4.0"}}
	s.Decline("v0.4.0")
	s.Decline("v0.4.0")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got := LoadState(path)
	if !got.HasDeclined("v0.4.0") || len(got.Declined) != 1 || got.Latest.Tag != "v0.4.0" {
		t.Errorf("round trip = %+v", got)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := LoadState(path); s.HasDeclined("v0.4.0") {
		t.Error("a corrupt state file was read")
	}
}
