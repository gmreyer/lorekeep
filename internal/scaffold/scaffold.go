// Package scaffold creates a new lore project.
//
// The template ships inside lorekeep rather than as a repository to clone, so
// a writer needs nothing but lorekeep.exe, and the template is versioned with
// the tooling that reads it.
//
// Git is optional. A project is complete without it, but the layout is always
// the one git handles well — plain text, one entity per file, derived output
// confined to build/ — so the git files can be added later without moving
// anything.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/project"
	"github.com/gmreyer/lorekeep/internal/schema"
)

// The embedded tree. Dotfiles are stored under inert names and mapped to
// their real names on write, so they never act on this repository.
//
//go:embed files
var files embed.FS

const tmplExt = ".tmpl"

// gitFiles maps each embedded git file to where it lands in a project.
var gitFiles = map[string]string{
	"files/git/gitattributes": ".gitattributes",
	"files/git/gitignore":     ".gitignore",
	"files/git/gitkeep":       path.Join(index.WorldDir, ".gitkeep"),
}

// CIFile is where the GitHub Actions workflow lands in a project.
var CIFile = path.Join(".github", "workflows", "build.yml")

const ciSource = "files/ci/build.yml"

// namePattern is the permitted shape of a project name, the same charset as
// an entity id so that it is safe in YAML, in a filename, and on a
// case-insensitive filesystem.
var namePattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// Options says what to create.
type Options struct {
	// Name is the project pack's name.
	Name string
	// Version is the lorekeep release the project is pinned to.
	Version string
	// Example adds a five-entity example world.
	Example bool
	// Git adds .gitattributes, .gitignore and world/.gitkeep.
	Git bool
	// CI adds a GitHub Actions workflow. It requires Git.
	CI bool
}

func (o Options) check() error {
	switch {
	case !namePattern.MatchString(o.Name):
		return fmt.Errorf("project name %q: use lowercase letters, digits and underscores", o.Name)
	case !project.IsRelease(o.Version):
		return fmt.Errorf("lorekeep version %q: not a release version like v1.2.3", o.Version)
	case o.CI && !o.Git:
		return errors.New("a CI workflow needs the git files too")
	}
	return nil
}

// Setup creates a project in dir and returns the paths it wrote, relative to
// dir and slash-separated.
//
// dir must be empty or not exist. Setup never overwrites anything, and if it
// fails part-way it removes what it wrote.
func Setup(dir string, o Options) ([]string, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	planned, err := plan(o)
	if err != nil {
		return nil, err
	}

	created, err := prepare(dir)
	if err != nil {
		return nil, err
	}
	written, err := write(dir, planned)
	if err != nil {
		if created {
			os.RemoveAll(dir)
		} else {
			for _, p := range written {
				os.Remove(filepath.Join(dir, filepath.FromSlash(p)))
			}
		}
		return nil, err
	}
	return written, nil
}

// plan renders every file the options ask for, keyed by destination path.
func plan(o Options) (map[string][]byte, error) {
	core, err := schema.CoreVersion()
	if err != nil {
		return nil, fmt.Errorf("core pack: %w", err)
	}
	data := struct{ Name, CoreVersion string }{o.Name, core}

	out := map[string][]byte{
		project.PinFile: []byte(o.Version + "\n"),
		// world/ must exist even when empty; a nil entry is a directory.
		index.WorldDir + "/": nil,
	}

	trees := []string{"files/base"}
	if o.Example {
		trees = append(trees, "files/example")
	}
	for _, root := range trees {
		err := fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			body, err := files.ReadFile(p)
			if err != nil {
				return err
			}
			dest := strings.TrimPrefix(p, root+"/")
			if strings.HasSuffix(dest, tmplExt) {
				dest = strings.TrimSuffix(dest, tmplExt)
				if body, err = render(p, body, data); err != nil {
					return err
				}
			}
			out[dest] = body
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if o.Git {
		for src, dest := range gitFiles {
			body, err := files.ReadFile(src)
			if err != nil {
				return nil, err
			}
			out[dest] = body
		}
	}
	if o.CI {
		body, err := files.ReadFile(ciSource)
		if err != nil {
			return nil, err
		}
		out[CIFile] = body
	}
	return out, nil
}

func render(name string, body []byte, data any) ([]byte, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(string(body))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// prepare makes sure dir exists and is empty, and reports whether it made it.
func prepare(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	case len(entries) > 0:
		return false, fmt.Errorf("%s is not empty; set up a new project in an empty folder", dir)
	}
	return false, nil
}

// write writes the planned files in a stable order and returns the paths of
// the files it wrote.
func write(dir string, planned map[string][]byte) ([]string, error) {
	paths := make([]string, 0, len(planned))
	for p := range planned {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var written []string
	for _, p := range paths {
		dest := filepath.Join(dir, filepath.FromSlash(p))
		body := planned[p]
		if body == nil && strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return written, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return written, err
		}
		// O_EXCL: never overwrite, even if something appeared since prepare.
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return written, err
		}
		_, werr := f.Write(body)
		cerr := f.Close()
		written = append(written, p)
		if werr != nil {
			return written, werr
		}
		if cerr != nil {
			return written, cerr
		}
	}
	return written, nil
}
