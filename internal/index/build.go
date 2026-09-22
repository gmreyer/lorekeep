package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmreyer/lore-core/internal/schema"
	"github.com/gmreyer/lore-core/internal/validate"
	"github.com/gmreyer/lore-core/internal/world"
)

// The names of the two derived artefacts and the directories they are built
// from. A world repository is a schema pack beside authored content; the build
// directory is derived and gitignored.
const (
	SchemaDir = "schema"
	WorldDir  = "world"
	BuildDir  = "build"

	IndexFile    = "index.db"
	SnapshotFile = "snapshot.json"
)

// Result is what one build produced.
type Result struct {
	Findings world.Findings
	Index    *Index
	// Written names the artefacts on disk, empty when validation failed.
	Written []string
}

// Build validates a world repository and, if it is sound, writes the index and
// the snapshot.
//
// Nothing is written when validation fails. A half-built index of a world with
// a dangling reference is worse than no index: it would answer questions, and
// the answers would be wrong.
func Build(repo, out string) (*Result, error) {
	pack, err := schema.LoadProject(filepath.Join(repo, SchemaDir))
	if err != nil {
		return nil, fmt.Errorf("schema pack: %w", err)
	}

	root := filepath.Join(repo, WorldDir)
	if err := checkWorldRoot(root); err != nil {
		return nil, err
	}

	w, parseFindings := world.Load(root)
	findings := append(world.Findings(nil), parseFindings...)
	findings = append(findings, validate.Validate(w, pack)...)
	findings.Sort()

	res := &Result{Findings: findings}
	if findings.HasErrors() {
		return res, nil
	}

	res.Index = Compile(w, pack)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return res, fmt.Errorf("creating the build directory: %w", err)
	}

	indexPath := filepath.Join(out, IndexFile)
	if err := WriteSQLite(res.Index, indexPath); err != nil {
		return res, err
	}
	snapshotPath := filepath.Join(out, SnapshotFile)
	if err := WriteSnapshot(res.Index, snapshotPath); err != nil {
		return res, err
	}

	res.Written = []string{indexPath, snapshotPath}
	return res, nil
}

// checkWorldRoot fails when the world content directory is absent or is not a
// directory.
//
// Either means the repository could not be read, like a missing schema pack,
// rather than a world with errors in it, so it is an error and not a finding.
// Git stores no empty directories: a world repo whose writer deleted every
// entity has no world/ after a fresh clone, and the message says how to keep
// one. An absent world/ is deliberately not an empty world, unlike an absent
// pack content file.
func checkWorldRoot(root string) error {
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("no %s/ directory; git stores no empty directories, so commit %s/.gitkeep "+
			"to keep an empty world", WorldDir, WorldDir)
	case err != nil:
		return fmt.Errorf("%s: %w", WorldDir, err)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", WorldDir)
	}
	return nil
}

// WriteSnapshot writes the JSON the runtime ships.
//
// It is indented rather than compact on purpose: at this scale the size costs
// nothing, and a snapshot whose diff can be read is a snapshot whose changes
// get reviewed.
func WriteSnapshot(idx *Index, path string) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the snapshot: %w", err)
	}
	// A trailing newline, so the file is a well-formed text file and a diff
	// does not end with "\ No newline at end of file".
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing the snapshot: %w", err)
	}
	return nil
}

// splitVisibility separates the authored form into the parts a query filters
// on. The whole string is stored too, because that is what an editor round
// trips back into a file.
func splitVisibility(v string) (kind, act string) {
	if before, after, found := strings.Cut(v, ":"); found {
		return before, after
	}
	return v, ""
}

func splitInterval(iv *Interval) (era any, earliest, latest any, precision any) {
	if iv == nil {
		return nil, nil, nil, nil
	}
	return nullableString(iv.Era), nullableInt(iv.Earliest), nullableInt(iv.Latest), nullableString(iv.Precision)
}

// nullableString keeps an unstated field NULL rather than empty, so that
// "no era was written down" and "the era is the empty string" cannot be
// confused by a query.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinAliases(aliases []string) string {
	return strings.Join(aliases, " ")
}
