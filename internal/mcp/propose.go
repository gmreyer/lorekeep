package mcp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/schema"
	"github.com/gmreyer/lorekeep/internal/world"
)

// ProposalsDir is where proposals are written, beside schema/ and world/. It
// is the only directory an agent's call writes to.
const ProposalsDir = "proposals"

// ErrInvalidProposal is a call rejected before anything is checked or
// written: a path outside world/, a file that is not an entity or statement,
// or no summary. One bad file rejects the whole call.
var ErrInvalidProposal = errors.New("proposal rejected")

// ProposedFile is one entity or statement file an agent proposes, by its
// path under world/.
type ProposedFile struct {
	Path    string `json:"path" jsonschema:"the file's path from the project root, starting world/ and ending .md, with forward slashes, e.g. world/characters/kaelen.md"`
	Content string `json:"content" jsonschema:"the whole file: YAML frontmatter and Markdown body"`
}

// ValidateResult is what validating proposed files found: the findings of the
// whole world with the files in place, and the neighbourhood of every entity
// they touch, for the caller to judge contradictions by. Nothing in it
// decides anything.
type ValidateResult struct {
	Findings []Finding `json:"findings"`
	// Blocking is set when a finding is an error: the world would not build.
	Blocking bool `json:"blocking"`
	// Neighbourhood is one hop around each entity a file declares, and
	// around the subject and object of each statement, read canon-wide on
	// the world with the files in place. It is read even when the world
	// would not build, so it may show what a dangling reference broke.
	Neighbourhood []resolve.Graph `json:"neighbourhood"`
	// NeighbourhoodNote says why the neighbourhood is empty when it could
	// not be read.
	NeighbourhoodNote string `json:"neighbourhood_note,omitempty"`
}

// noNeighbourhood is the note for a neighbourhood the resolver could not read
// from a world that does not build.
const noNeighbourhood = "the neighbourhood could not be read because the world does not build with these files"

// ProposeResult names the proposal that was written.
type ProposeResult struct {
	ID string `json:"id"`
	// Dir is the proposal's directory relative to the project.
	Dir      string    `json:"dir"`
	Files    []string  `json:"files"`
	Findings []Finding `json:"findings"`
	// Blocking is set when the proposal has errors. It is written anyway,
	// so the writer sees what was tried.
	Blocking bool   `json:"blocking"`
	Note     string `json:"note,omitempty"`
}

// proposalMeta is proposals/<id>/proposal.json.
type proposalMeta struct {
	Summary         string   `json:"summary"`
	Files           []string `json:"files"`
	Created         string   `json:"created"`
	LorekeepVersion string   `json:"lorekeep_version"`
}

// overlay is the world with proposed files in place.
type overlay struct {
	findings world.Findings
	// idx is the overlaid world compiled, even when it has errors.
	idx *index.Index
	// unvalidated is set when idx was compiled from a world that failed
	// validation.
	unvalidated bool
	// roots are the entities the files touch, sorted.
	roots []string
}

// validate checks proposed files against the world without writing anything
// under the project.
func (s *Server) validate(ctx context.Context, files []ProposedFile) (ValidateResult, error) {
	ov, err := s.overlay(ctx, files)
	if err != nil {
		return ValidateResult{}, err
	}
	graphs, note := s.neighbourhood(ov)
	return ValidateResult{
		Findings:          findings(ov.findings),
		Blocking:          ov.findings.HasErrors(),
		Neighbourhood:     graphs,
		NeighbourhoodNote: note,
	}, nil
}

// propose writes proposed files under proposals/<id>/ for a human to review.
// It writes even when the files have blocking findings, and says so. It
// never writes world/ or schema/.
func (s *Server) propose(ctx context.Context, files []ProposedFile, summary string) (ProposeResult, error) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ProposeResult{}, fmt.Errorf("%w: a summary is required", ErrInvalidProposal)
	}
	ov, err := s.overlay(ctx, files)
	if err != nil {
		return ProposeResult{}, err
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	slices.Sort(paths)

	now := time.Now().UTC()
	id, dir, err := s.proposalDir(now)
	if err != nil {
		return ProposeResult{}, err
	}
	if err := writeProposal(dir, files, proposalMeta{
		Summary:         summary,
		Files:           paths,
		Created:         now.Format(time.RFC3339),
		LorekeepVersion: lorekeepVersion(),
	}, ov.findings); err != nil {
		_ = os.RemoveAll(dir) // created by this call, holding only its files
		return ProposeResult{}, err
	}

	out := ProposeResult{
		ID:       id,
		Dir:      path.Join(ProposalsDir, id),
		Files:    paths,
		Findings: findings(ov.findings),
		Blocking: ov.findings.HasErrors(),
	}
	if out.Blocking {
		out.Note = "written with blocking findings, so the writer sees what was tried; " +
			"the world would not build with these files"
	}
	return out, nil
}

// overlay checks the call, copies schema/ and world/ into a temporary
// directory, puts the files in place, and validates and compiles the result.
// Nothing under the project is written.
func (s *Server) overlay(ctx context.Context, files []ProposedFile) (overlay, error) {
	if err := ctx.Err(); err != nil {
		return overlay{}, err
	}
	roots, err := s.checkFiles(files)
	if err != nil {
		return overlay{}, err
	}

	tmp, err := os.MkdirTemp("", "lorekeep-proposal-*")
	if err != nil {
		return overlay{}, err
	}
	defer os.RemoveAll(tmp)

	// The read lock keeps a reload from running during the copy. It does
	// not stop a writer's editor; the copy is as good as a build's read.
	s.mu.RLock()
	err = copyTree(filepath.Join(s.repo, index.SchemaDir), filepath.Join(tmp, index.SchemaDir), false)
	if err == nil {
		err = copyTree(filepath.Join(s.repo, index.WorldDir), filepath.Join(tmp, index.WorldDir), true)
	}
	s.mu.RUnlock()
	if err != nil {
		return overlay{}, fmt.Errorf("copying the world: %w", err)
	}

	for _, f := range files {
		dst := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return overlay{}, err
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return overlay{}, err
		}
	}

	res, err := index.Load(tmp)
	if err != nil {
		return overlay{}, err
	}
	ov := overlay{findings: res.Findings, idx: res.Index, roots: roots}
	if ov.idx == nil {
		// The world does not build, but the advisory lane still wants the
		// neighbourhood. The resolver reads a dangling reference as absent.
		pack, err := schema.LoadProject(filepath.Join(tmp, index.SchemaDir))
		if err != nil {
			return overlay{}, fmt.Errorf("schema pack: %w", err)
		}
		w, _ := world.Load(filepath.Join(tmp, index.WorldDir))
		ov.idx = index.Compile(w, pack)
		ov.unvalidated = true
	}
	return ov, nil
}

// neighbourhood expands one hop around each root on the overlaid world,
// omnisciently and across every status the role reads. A root that is not
// present, and a role that may not read omnisciently, give no graph. The
// note is set when the neighbourhood could not be read at all.
func (s *Server) neighbourhood(ov overlay) (out []resolve.Graph, note string) {
	out = []resolve.Graph{}
	var statuses []string
	for _, st := range slices.Sorted(maps.Keys(s.pol.statuses)) {
		statuses = append(statuses, string(st))
	}
	rctx, err := s.readContext(nil, "", statuses)
	if err != nil {
		return out, ""
	}
	if ov.unvalidated {
		// The resolver is written for validated worlds, and this one is
		// not: no test found an input that breaks it, but nothing promises
		// one does not exist, and the SDK does not recover a handler's
		// panic, so one bad proposal would end the server. Only this path
		// is guarded; a panic over a validated world is a bug to see.
		defer func() {
			if recover() != nil {
				out, note = []resolve.Graph{}, noNeighbourhood
			}
		}()
	}
	res := resolve.New(ov.idx)
	for _, id := range ov.roots {
		g, err := res.Expand(rctx, id, 1, nil)
		if err != nil {
			continue
		}
		out = append(out, g)
	}
	return out, ""
}

// checkFiles rejects the call unless every path is confined to world/ and
// every file is an entity or a statement. It returns the entities the files
// touch: each entity's ID, and each statement's subject and object.
func (s *Server) checkFiles(files []ProposedFile) ([]string, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: no files", ErrInvalidProposal)
	}
	worldRoot, err := filepath.EvalSymlinks(filepath.Join(s.repo, index.WorldDir))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", index.WorldDir, err)
	}

	seen := map[string]bool{}
	roots := map[string]bool{}
	for _, f := range files {
		if err := checkPath(f.Path); err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrInvalidProposal, f.Path, err)
		}
		if err := confined(s.repo, worldRoot, f.Path); err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrInvalidProposal, f.Path, err)
		}
		key := strings.ToLower(f.Path)
		if seen[key] {
			return nil, fmt.Errorf("%w: %q is given twice", ErrInvalidProposal, f.Path)
		}
		seen[key] = true

		// D6: the world parser decides what a file is, and only an entity
		// or a statement may be proposed.
		name := strings.TrimPrefix(f.Path, index.WorldDir+"/")
		doc, parsed := world.Parse(name, []byte(f.Content))
		switch {
		case doc.Entity != nil:
			roots[doc.Entity.ID] = true
		case doc.Statement != nil:
			roots[doc.Statement.Subject] = true
			roots[doc.Statement.Object] = true
		default:
			for i := range parsed {
				parsed[i].File = f.Path
			}
			return nil, fmt.Errorf("%w: %q is not an entity or statement file:\n%s",
				ErrInvalidProposal, f.Path, world.Findings(parsed))
		}
	}
	delete(roots, "")
	return slices.Sorted(maps.Keys(roots)), nil
}

// checkPath accepts a relative, clean, forward-slash path under world/ to a
// .md file in a folder the world loader reads.
func checkPath(p string) error {
	switch {
	case p == "":
		return errors.New("empty path")
	case strings.Contains(p, `\`):
		return errors.New("use forward slashes")
	case strings.Contains(p, ":"):
		return errors.New("a path is relative to the project")
	case strings.Contains(p, ".."):
		return errors.New("a path may not contain ..")
	case path.IsAbs(p) || filepath.IsAbs(p) || filepath.VolumeName(p) != "":
		return errors.New("a path is relative to the project")
	case path.Clean(p) != p:
		return errors.New("the path is not clean")
	case !strings.HasPrefix(p, index.WorldDir+"/"):
		return fmt.Errorf("only files under %s/ may be proposed", index.WorldDir)
	case !strings.HasSuffix(p, ".md"):
		return errors.New("only .md files may be proposed")
	}
	segs := strings.Split(p, "/")
	for _, seg := range segs[1:] {
		// The world loader skips these folders; a file in one would be
		// proposed but never checked.
		if strings.HasPrefix(seg, ".") || seg == index.BuildDir {
			return fmt.Errorf("the world loader does not read %q", seg)
		}
	}
	return nil
}

// confined checks that p stays inside world/. No part of it that exists may
// be a link: filepath.EvalSymlinks does not resolve a Windows junction, so a
// link is refused rather than followed. Then, as a second check, p with
// every symlink resolved must land inside world/. The file and its folders
// need not exist yet.
func confined(repo, worldRoot, p string) error {
	cur := repo
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		last := i == len(segs)-1
		switch {
		case info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			return fmt.Errorf("%q is a link", path.Join(segs[:i+1]...))
		case !last && !info.IsDir():
			return fmt.Errorf("%q is not a folder", path.Join(segs[:i+1]...))
		case last && !info.Mode().IsRegular():
			return errors.New("not a plain file")
		}
	}

	full := filepath.Join(repo, filepath.FromSlash(p))
	existing, rest := full, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return errors.New("cannot resolve the path")
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	if !within(worldRoot, filepath.Join(real, rest)) {
		return fmt.Errorf("resolves outside %s/", index.WorldDir)
	}
	return nil
}

// within reports whether p is root or below it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyTree copies the regular files under src into dst, following a
// symlinked file but not a symlinked folder, as the world loader does. With
// asWorld, it also skips the folders the world loader skips.
func copyTree(src, dst string, asWorld bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == src && errors.Is(err, fs.ErrNotExist) {
				return nil // index.Load reports it
			}
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if name := d.Name(); asWorld && p != src && (name == index.BuildDir || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return nil // a broken link or a linked folder: the loader skips it too
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// proposalDir creates proposals/<id>/ exclusively and returns the ID and the
// directory. An ID is p_<yyyymmdd>_<hhmmss>_<4 hex>, in UTC; a taken one is
// never reused.
func (s *Server) proposalDir(now time.Time) (string, string, error) {
	parent := filepath.Join(s.repo, ProposalsDir)
	if err := os.Mkdir(parent, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", "", err
	}
	// proposals/ must be a real folder of this project: a link could lead
	// the write into world/ or out of the project.
	info, err := os.Lstat(parent)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return "", "", fmt.Errorf("%s/ is not a plain folder; refusing to write through it", ProposalsDir)
	}
	realRepo, err := filepath.EvalSymlinks(s.repo)
	if err != nil {
		return "", "", err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", "", err
	}
	if rel, err := filepath.Rel(realRepo, realParent); err != nil || rel != ProposalsDir {
		return "", "", fmt.Errorf("%s/ resolves outside the project; refusing to write through it", ProposalsDir)
	}

	stamp := now.Format("20060102_150405")
	for range 32 {
		var b [2]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", "", err
		}
		id := fmt.Sprintf("p_%s_%04x", stamp, binary.BigEndian.Uint16(b[:]))
		dir := filepath.Join(parent, id)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", err
		}
	}
	return "", "", errors.New("no free proposal ID; try again")
}

// writeProposal writes the files, proposal.json and validation.txt into dir,
// a directory this call created.
func writeProposal(dir string, files []ProposedFile, meta proposalMeta, found world.Findings) error {
	for _, f := range files {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "proposal.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "validation.txt"), []byte(validationText(found)), 0o644)
}

// validationText formats findings as lorekeep build prints them, with a tally.
func validationText(fs world.Findings) string {
	var b strings.Builder
	for _, f := range fs {
		located := f
		located.File = path.Join(index.WorldDir, f.File)
		fmt.Fprintf(&b, "%-7s %s\n", f.Severity, located.String())
	}
	if len(fs) > 0 {
		b.WriteString("\n")
	}
	errs := fs.Errors()
	b.WriteString(plural(errs, "error", "errors"))
	if warns := len(fs) - errs; warns > 0 {
		b.WriteString(", " + plural(warns, "warning", "warnings"))
	}
	b.WriteString("\n")
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// lorekeepVersion is the module version this binary was built from, read the
// way cmd/lorekeep reads it.
func lorekeepVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(unknown)"
	}
	return info.Main.Version
}
