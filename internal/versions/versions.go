// Package versions installs and finds lorekeep releases on this machine.
//
// A project pins the lorekeep that builds it, and a writer may have several
// projects on different pins, so every release that is needed is kept side by
// side in a per-user cache. Releases are public GitHub release assets; each is
// checked against the release's SHA256SUMS before it lands in the cache, and a
// failed or tampered download leaves nothing behind.
package versions

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gmreyer/lorekeep/internal/project"
)

// Default endpoints and names for the public lorekeep releases.
const (
	DefaultDownloads = "https://github.com/gmreyer/lorekeep/releases/download"
	DefaultAPI       = "https://api.github.com/repos/gmreyer/lorekeep"
	Asset            = "lorekeep.exe"
	sumsAsset        = "SHA256SUMS"
)

// Manager holds the cache and the endpoints it downloads from.
type Manager struct {
	// Dir holds one subdirectory per installed version.
	Dir string
	// Downloads is the base URL of release assets: <Downloads>/<tag>/<asset>.
	Downloads string
	// API is the GitHub REST base URL of the repository.
	API  string
	HTTP *http.Client
}

// New returns a Manager for the per-user cache and the public releases.
func New() (*Manager, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return &Manager{
		Dir:       filepath.Join(cache, "lorekeep", "versions"),
		Downloads: DefaultDownloads,
		API:       DefaultAPI,
		HTTP:      &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// Path is where version v's binary lives in the cache.
func (m *Manager) Path(v string) string {
	return filepath.Join(m.Dir, v, Asset)
}

// Installed reports whether version v is in the cache.
func (m *Manager) Installed(v string) bool {
	info, err := os.Stat(m.Path(v))
	return err == nil && info.Mode().IsRegular()
}

// Install downloads version v into the cache, checked against the release's
// SHA256SUMS. It does nothing if v is already installed.
func (m *Manager) Install(ctx context.Context, v string) error {
	if !project.IsRelease(v) {
		return fmt.Errorf("%q is not a release version", v)
	}
	if m.Installed(v) {
		return nil
	}

	sums, err := m.get(ctx, v, sumsAsset)
	if err != nil {
		return err
	}
	want, err := findSum(sums, Asset)
	if err != nil {
		return fmt.Errorf("lorekeep %s: %w", v, err)
	}

	resp, err := m.open(ctx, v, Asset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return m.place(v, resp.Body, want)
}

// InstallFrom copies a local binary into the cache as version v, without
// checking it against a release. It is for the running binary, which is
// already on this machine and is what the writer chose to run.
func (m *Manager) InstallFrom(v, exe string) error {
	if m.Installed(v) {
		return nil
	}
	f, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer f.Close()
	return m.place(v, f, "")
}

// place streams r into a temporary file beside its destination, checks its
// hash when want is not empty, and renames it into place.
func (m *Manager) place(v string, r io.Reader, want string) error {
	dir := filepath.Dir(m.Path(v))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, Asset+".*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed

	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("lorekeep %s: download: %w", v, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && got != want {
		return fmt.Errorf("lorekeep %s: downloaded %s does not match SHA256SUMS (got %s, want %s); nothing was installed",
			v, Asset, got, want)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.Path(v))
}

// findSum returns the hash SHA256SUMS lists for name.
func findSum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s does not list %s", sumsAsset, name)
}

func (m *Manager) get(ctx context.Context, v, asset string) ([]byte, error) {
	resp, err := m.open(ctx, v, asset)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (m *Manager) open(ctx context.Context, v, asset string) (*http.Response, error) {
	url := m.Downloads + "/" + v + "/" + asset
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lorekeep %s: %w", v, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("lorekeep %s: no such release, or it has no %s", v, asset)
		}
		return nil, fmt.Errorf("lorekeep %s: %s: %s", v, asset, resp.Status)
	}
	return resp, nil
}

// Release is a published lorekeep release.
type Release struct {
	Tag   string `json:"tag"`
	Notes string `json:"notes"`
}

// Latest returns the newest published release. GitHub's latest-release
// endpoint already leaves out drafts and pre-releases.
func (m *Manager) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.API+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("latest release: %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("latest release: %w", err)
	}
	if !project.IsRelease(body.TagName) {
		return Release{}, fmt.Errorf("latest release: unexpected tag %q", body.TagName)
	}
	return Release{Tag: body.TagName, Notes: body.Body}, nil
}

// NoDispatchEnv is set in the environment of every binary lorekeep runs from
// the cache. The child then runs as itself, whatever the project pins: it is
// either the pinned version already, or the candidate an update is testing.
// It is also what stops two binaries dispatching to each other forever.
const NoDispatchEnv = "LOREKEEP_NO_DISPATCH"

// Run runs installed version v with args and returns its exit code.
func (m *Manager) Run(v string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(m.Path(v), args...)
	cmd.Env = append(os.Environ(), NoDispatchEnv+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("lorekeep %s: %w", v, err)
	}
	return 0, nil
}
