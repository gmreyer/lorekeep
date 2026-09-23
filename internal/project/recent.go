package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxRecent is how many projects the recent list keeps.
const MaxRecent = 8

// IsProject reports whether dir holds a lorekeep project: a schema/ and a
// world/ directory, the two things a build cannot do without.
func IsProject(dir string) bool {
	for _, sub := range []string{"schema", "world"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// RecentPath is the default location of the recent-projects file. It is per
// user, like the version cache, and never inside a project.
func RecentPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lorekeep", "projects.json"), nil
}

// LoadRecent returns the recently opened projects, newest first, leaving out
// any that are no longer projects. A missing or unreadable file is an empty
// list.
func LoadRecent(path string) []string {
	var list []string
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &list) != nil {
		return nil
	}
	return slices.DeleteFunc(list, func(dir string) bool { return !IsProject(dir) })
}

// AddRecent moves dir to the front of the recent list and saves it.
func AddRecent(path, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	list := slices.DeleteFunc(LoadRecent(path), func(d string) bool {
		// Windows paths compare case-insensitively.
		return strings.EqualFold(d, abs)
	})
	list = append([]string{abs}, list...)
	if len(list) > MaxRecent {
		list = list[:MaxRecent]
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
