package versions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// CheckInterval is how often lorekeep asks GitHub for the latest release.
const CheckInterval = 24 * time.Hour

// State is what lorekeep remembers between runs about updates. It lives in
// the per-user config directory, never in a project: whether a writer said no
// to a version is theirs, not the project's.
type State struct {
	LastCheck time.Time `json:"last_check"`
	// Latest is the release the last check found, so an offer can still be
	// made between checks and offline.
	Latest *Release `json:"latest,omitempty"`
	// Declined lists versions the writer said no to. They are not offered
	// again, but lorekeep update still installs them on request.
	Declined []string `json:"declined,omitempty"`
}

// StatePath is the default location of the state file.
func StatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lorekeep", "state.json"), nil
}

// LoadState reads the state file. A missing or unreadable file is an empty
// state: forgetting a check costs one request, and must never stop a build.
func LoadState(path string) State {
	var s State
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

// Save writes the state file.
func (s State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// HasDeclined reports whether the writer said no to version v.
func (s State) HasDeclined(v string) bool {
	return slices.Contains(s.Declined, v)
}

// Decline records that the writer said no to version v.
func (s *State) Decline(v string) {
	if !s.HasDeclined(v) {
		s.Declined = append(s.Declined, v)
	}
}

// Refresh asks for the latest release when the last check is older than
// CheckInterval, and returns the newest release known, which may be none.
// A failed request is not an error: it is retried after the next interval,
// and the last known release stands meanwhile.
func (m *Manager) Refresh(ctx context.Context, s *State, now time.Time) *Release {
	if now.Sub(s.LastCheck) < CheckInterval {
		return s.Latest
	}
	s.LastCheck = now
	if r, err := m.Latest(ctx); err == nil {
		s.Latest = &r
	}
	return s.Latest
}
