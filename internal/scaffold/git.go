package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/gmreyer/lorekeep/internal/index"
)

// GitResult says what AddGit did with each file, by slash-separated path.
type GitResult struct {
	// Written were missing, or differed and the caller agreed to replace them.
	Written []string
	// Present already had exactly the template's contents.
	Present []string
	// Kept differ from the template and were left as they are.
	Kept []string
}

// AddGit prepares an existing project for git: .gitattributes, .gitignore and
// world/.gitkeep, plus the GitHub Actions workflow when ci is set. It never
// runs git.
//
// A missing file is written. A file that exists with other contents is the
// writer's, and is replaced only when replace returns true for its path; a
// nil replace keeps every such file.
func AddGit(dir string, ci bool, replace func(path string) bool) (GitResult, error) {
	var res GitResult
	for _, sub := range []string{index.SchemaDir, index.WorldDir} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			return res, fmt.Errorf("%s is not a lorekeep project: no %s/ directory", dir, sub)
		}
	}

	planned, err := gitPlan(ci)
	if err != nil {
		return res, err
	}
	paths := make([]string, 0, len(planned))
	for p := range planned {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		dest := filepath.Join(dir, filepath.FromSlash(p))
		want := planned[p]
		have, err := os.ReadFile(dest)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return res, err
		case bytes.Equal(have, want):
			res.Present = append(res.Present, p)
			continue
		case replace == nil || !replace(p):
			res.Kept = append(res.Kept, p)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(dest, want, 0o644); err != nil {
			return res, err
		}
		res.Written = append(res.Written, p)
	}
	return res, nil
}
