package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/world"
)

// policy is what a role permits. It is fixed at launch; no tool argument
// reaches it.
type policy struct {
	// ceiling is the visibility every read is made at.
	ceiling world.VisibilityKind
	// omniscient allows a read without a knower: canon truth.
	omniscient bool
	// statuses are the statuses a caller may ask for. Canon is read when the
	// caller names none.
	statuses map[world.Status]bool
}

// roles are the session roles a server can be launched with. Under D2 the
// author reads at internal, the only visibility the resolver supports; a
// narrower ceiling arrives with the spoiler filter.
var roles = map[Role]policy{
	RoleAuthor: {
		ceiling:    world.VisibilityInternal,
		omniscient: true,
		statuses:   map[world.Status]bool{world.StatusCanon: true, world.StatusDraft: true},
	},
}

// Errors a read can fail with before it reaches the resolver.
var (
	// ErrUnknownRole is a role the server does not define.
	ErrUnknownRole = errors.New("unknown session role")
	// ErrKnowerRequired is a read without a knower under a role that does
	// not allow omniscient reads.
	ErrKnowerRequired = errors.New("this session role needs a knower for every read")
	// ErrStatusNotAllowed is a status the session role may not read.
	ErrStatusNotAllowed = errors.New("status not readable in this session role")
	// ErrNoIndex is a read before any build of the world has succeeded.
	ErrNoIndex = errors.New("the world has not built yet")
)

// Server serves one project directory. It keeps the compiled index and a
// resolver over it in memory, and rebuilds both, with build/index.db, when a
// file under schema/ or world/ changes.
type Server struct {
	repo string
	role Role
	pol  policy
	mcp  *sdk.Server

	// mu guards the state below. A reload takes it for writing; a read holds
	// it for reading for its whole length, so build/index.db is never
	// rewritten under a search.
	mu sync.RWMutex
	// stamp is the latest modification time under schema/ and world/ when
	// the last build started.
	stamp time.Time
	// cur is the last good build; nil until one succeeds.
	cur *loaded
	// report describes the last build when it failed, and is empty when it
	// succeeded.
	report Report
}

// loaded is one good build: the index, the resolver over it, and the search
// database written beside it.
type loaded struct {
	idx *index.Index
	res *resolve.Resolver
	db  string
	// subjects maps a statement ID to its subject, to re-read a statement
	// search hit through the entity it is about.
	subjects map[string]string
}

// Report is attached to every result while the last build of the world
// failed: the server is still answering from the last good build.
type Report struct {
	BuildError    string    `json:"build_error,omitempty" jsonschema:"why the last build could not run; answers come from the last good build"`
	BuildFindings []Finding `json:"build_findings,omitempty" jsonschema:"what failed the last build; answers come from the last good build"`
}

func (r *Report) setReport(x Report) { *r = x }

func (r Report) failed() bool { return r.BuildError != "" || len(r.BuildFindings) > 0 }

// String renders a report for a tool error, which carries text only.
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("the last build failed; answering from the last good build")
	if r.BuildError != "" {
		b.WriteString(": ")
		b.WriteString(r.BuildError)
	}
	for _, f := range r.BuildFindings {
		fmt.Fprintf(&b, "\n  %s %s", f.Severity, f.String())
	}
	return b.String()
}

// Finding is one validation finding, with its file relative to the project.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}

func (f Finding) String() string {
	return world.Finding{File: f.File, Line: f.Line, Path: f.Path, Msg: f.Message, Code: world.Code(f.Code)}.String()
}

func findings(fs world.Findings) []Finding {
	out := make([]Finding, 0, len(fs))
	for _, f := range fs {
		out = append(out, Finding{
			Severity: string(f.Severity),
			Code:     string(f.Code),
			File:     filepath.ToSlash(filepath.Join(index.WorldDir, filepath.FromSlash(f.File))),
			Line:     f.Line,
			Path:     f.Path,
			Message:  f.Msg,
		})
	}
	return out
}

// New builds the world under cfg.Repo and returns a server over it. An
// unknown role, or a project whose schema or world cannot be read at all,
// fails here. A world with errors does not: the server starts, and every
// read reports the findings until a build succeeds.
func New(cfg Config) (*Server, error) {
	pol, ok := roles[cfg.Role]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRole, cfg.Role)
	}
	s := &Server{repo: cfg.Repo, role: cfg.Role, pol: pol}
	if err := s.reload(true); err != nil {
		return nil, err
	}
	if s.report.BuildError != "" {
		return nil, errors.New(s.report.BuildError)
	}
	s.mcp = sdk.NewServer(implementation, &sdk.ServerOptions{Instructions: instructions})
	s.register()
	return s, nil
}

// reload rebuilds the world when a file under schema/ or world/ changed since
// the last build, or always when force is set.
//
// The stamp is taken before the build, so an edit made during it is seen by
// the next call. A failed build keeps the last good one and records why.
func (s *Server) reload(force bool) error {
	stamp, err := latestMtime(filepath.Join(s.repo, index.SchemaDir), filepath.Join(s.repo, index.WorldDir))
	if err != nil {
		return err
	}
	if !force && stamp.Equal(s.stamp) {
		return nil
	}
	s.stamp = stamp

	out := filepath.Join(s.repo, index.BuildDir)
	res, err := index.Build(s.repo, out)
	switch {
	case err != nil:
		s.report = Report{BuildError: err.Error()}
	case res.Index == nil:
		s.report = Report{BuildFindings: findings(res.Findings)}
	default:
		subjects := make(map[string]string, len(res.Index.Statements))
		for _, st := range res.Index.Statements {
			subjects[st.ID] = st.Subject
		}
		s.cur = &loaded{
			idx:      res.Index,
			res:      resolve.New(res.Index),
			db:       filepath.Join(out, index.IndexFile),
			subjects: subjects,
		}
		s.report = Report{}
	}
	return nil
}

// latestMtime returns the latest modification time of any file or directory
// under the roots. Directories count, so a removed file is a change. A root
// that does not exist is skipped; the build reports it.
func latestMtime(roots ...string) (time.Time, error) {
	var latest time.Time
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == root && errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil // removed while walking; its directory moved too
				}
				return err
			}
			if t := info.ModTime(); t.After(latest) {
				latest = t
			}
			return nil
		})
		if err != nil {
			return time.Time{}, fmt.Errorf("checking for changes: %w", err)
		}
	}
	return latest, nil
}

// read runs fn against the current build, rebuilding first if the world
// changed. fn runs under the read lock.
func (s *Server) read(fn func(cur *loaded) error) (Report, error) {
	s.mu.Lock()
	err := s.reload(false)
	s.mu.Unlock()
	if err != nil {
		return Report{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	report := s.report
	if s.cur == nil {
		return report, withReport(ErrNoIndex, report)
	}
	if err := fn(s.cur); err != nil {
		return report, withReport(err, report)
	}
	return report, nil
}

// withReport adds a failed build's report to an error, since a tool error
// carries text only.
func withReport(err error, r Report) error {
	if !r.failed() {
		return err
	}
	return fmt.Errorf("%w\n%s", err, r)
}

// readContext is the one place a resolve.Context is built for a tool. The
// visibility is the role's ceiling; the caller chooses only the worldline,
// the knower, and statuses the role allows. Without a knower the read is
// omniscient, and only if the role allows it.
func (s *Server) readContext(wl map[string]string, knower string, statuses []string) (resolve.Context, error) {
	opts, err := s.options(statuses)
	if err != nil {
		return resolve.Context{}, err
	}
	if knower != "" {
		return resolve.Scoped(resolve.Worldline(wl), knower, opts...), nil
	}
	if !s.pol.omniscient {
		return resolve.Context{}, fmt.Errorf("%w (role %s)", ErrKnowerRequired, s.role)
	}
	return resolve.Omniscient(resolve.Worldline(wl), opts...), nil
}

// options are the resolver options for a read: the role's ceiling, and the
// statuses asked for if the role allows every one of them.
func (s *Server) options(statuses []string) ([]resolve.Option, error) {
	opts := []resolve.Option{resolve.WithVisibility(s.pol.ceiling)}
	if len(statuses) == 0 {
		return opts, nil
	}
	sts := make([]world.Status, 0, len(statuses))
	for _, st := range statuses {
		if !s.pol.statuses[world.Status(st)] {
			return nil, fmt.Errorf("%w: %q (allowed: %s)", ErrStatusNotAllowed, st, s.allowedStatuses())
		}
		sts = append(sts, world.Status(st))
	}
	return append(opts, resolve.WithStatuses(sts...)), nil
}

func (s *Server) allowedStatuses() string {
	names := make([]string, 0, len(s.pol.statuses))
	for st := range maps.Keys(s.pol.statuses) {
		names = append(names, string(st))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// instructions tell a client how reads are scoped, and what a world file looks
// like, so a first proposal is well formed. The format reference names no
// type, relation or era: those come from the project's schema packs, which
// get_world_index lists.
const instructions = `lorekeep serves one fictional world's lore graph. Every read takes:
- worldline: decision ID -> outcome, choosing a story branch. An omitted decision is unassigned, so only what holds in every branch shows. get_world_index lists the decisions and their outcomes.
- knower: an agent ID (a character or faction). With one, the read shows the world as that agent believes it; without one, it shows canon truth, if the session role allows.
- statuses: canon by default; add draft to see drafts.
Nothing here writes the world: propose_change writes a proposal for a human to review.

File format. Each entity and each statement is one Markdown file under world/: YAML frontmatter between --- lines, then prose. <...> is a placeholder; get_world_index lists the types, relations, eras and decisions, and get_entity without a knower returns an existing file as source. Optional keys are left out when unused.

An entity file:
---
id: <new ID: a-z, 0-9 and _, opaque, never derived from the name; IDs never change>
type: <entity type>
name: <display name>
aliases: [<another name>]                 # optional
status: draft                             # draft | canon | deprecated | non_canon
visibility: internal                      # public | spoiler | internal
relations:                                # optional; only relations the schema allows between the two types
  - { type: <relation>, target: <entity ID>, priority: 1, note: <text>, valid_in: [<condition>] }  # priority, note, valid_in optional
beliefs:                                  # optional, agents only: what this agent holds a statement to be
  - { statement: <statement ID>, value: true, confidence: high, since: <interval>, acquired_from: <agent ID>, valid_in: [<condition>] }
asserts:                                  # optional, agents only: what it says; saying what it does not believe is a lie
  - { statement: <statement ID>, value: false, to: <agent ID>, when: <interval>, valid_in: [<condition>] }
lifespan: <interval>                      # optional, characters only
date: <interval>                          # optional, events only
outcomes: [<outcome>, <outcome>]          # decisions only, required
valid_in: [<condition>]                   # optional; omitted means every branch
---
Prose about the entity.

A statement file, only for a fact some agent can be wrong about; any other fact is a relation:
---
id: <new ID>
type: statement
name: <the fact as a sentence>
subject: <entity ID>
relation: <relation>
object: <entity ID>
truth: true                               # canon truth: true | false | unresolved
common: true                              # optional: agents with no belief of their own take the truth
status: draft
visibility: internal
valid_in: [<condition>]                   # optional
---
Prose about the fact.

<interval> is { era: <era>, earliest: <year>, latest: <year>, precision: exact }, with precision exact or approximate; omit a bound that is unknown.
<condition> is { decision: <decision ID>, outcome: <outcome> }; several in one valid_in must all hold.
confidence is high, medium or low.`
