package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gmreyer/lorekeep/internal/index"
	"github.com/gmreyer/lorekeep/internal/resolve"
	"github.com/gmreyer/lorekeep/internal/schema"
)

// Search result limits. index.Search returns at most maxSearchLimit hits;
// the server asks for all of them, since some are dropped as absent.
const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50
)

// Scope is what every read takes besides its own arguments. There is no
// visibility here, and no switch to omniscience: those come from the session
// role. An argument the schema does not list is rejected.
type Scope struct {
	Worldline map[string]string `json:"worldline,omitempty" jsonschema:"story branch: decision ID to outcome; an omitted decision is unassigned"`
	Knower    string            `json:"knower,omitempty" jsonschema:"agent ID to read as; omit for canon truth if the session role allows it"`
	Statuses  []string          `json:"statuses,omitempty" jsonschema:"statuses to read; canon when omitted; add draft to see drafts"`
}

type (
	worldIndexArgs struct {
		Scope
	}
	searchArgs struct {
		Scope
		Query string `json:"query" jsonschema:"words to find in names, aliases and prose; all must match"`
		Limit int    `json:"limit,omitempty" jsonschema:"most hits to return, 1-50; default 10"`
	}
	entityArgs struct {
		Scope
		ID string `json:"id" jsonschema:"entity ID"`
	}
	expandArgs struct {
		Scope
		ID        string   `json:"id" jsonschema:"entity ID to start from"`
		Depth     int      `json:"depth,omitempty" jsonschema:"hops to walk, 1-3; default 1"`
		Relations []string `json:"relations,omitempty" jsonschema:"relation or inverse names to follow; all when omitted"`
	}
	beliefsArgs struct {
		Scope
		Agent string `json:"agent" jsonschema:"agent whose beliefs to list; with a knower, only the knower itself"`
	}
	diffArgs struct {
		A        map[string]string `json:"a" jsonschema:"first worldline: decision ID to outcome"`
		B        map[string]string `json:"b" jsonschema:"second worldline: decision ID to outcome"`
		Knower   string            `json:"knower,omitempty" jsonschema:"agent ID; a diff is an authoring read, so a knower is refused"`
		Statuses []string          `json:"statuses,omitempty" jsonschema:"statuses to read; canon when omitted; add draft to see drafts"`
	}
	validateArgs struct {
		Files []ProposedFile `json:"files" jsonschema:"entity and statement files to check"`
	}
	proposeArgs struct {
		Files   []ProposedFile `json:"files" jsonschema:"entity and statement files to propose"`
		Summary string         `json:"summary" jsonschema:"one line saying what the proposal changes"`
	}
)

// WorldIndex is the map of the world under a context.
type WorldIndex struct {
	Types []TypeGroup `json:"types"`
	// Decisions are read omnisciently when the role allows, so a caller can
	// name a worldline before it has one.
	Decisions []Decision      `json:"decisions"`
	Eras      []index.Ordered `json:"eras"`
	Relations []RelationInfo  `json:"relations"`
	Report
}

// TypeGroup is the entities of one type, sorted by ID.
type TypeGroup struct {
	Type     string            `json:"type"`
	Entities []resolve.Summary `json:"entities"`
}

// Decision is a decision and the outcomes a worldline can give it.
type Decision struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Outcomes []string `json:"outcomes"`
}

// RelationInfo is one relation of the vocabulary.
type RelationInfo struct {
	Name      string `json:"name"`
	Inverse   string `json:"inverse,omitempty"`
	Symmetric bool   `json:"symmetric,omitempty"`
	Role      string `json:"role,omitempty"`
}

// SearchResult is the hits present under the context, best first. Absent
// hits are left out without a trace: a count of them would say something
// exists outside the caller's story.
type SearchResult struct {
	Hits []SearchHit `json:"hits"`
	Report
}

// SearchHit is one hit as the resolver reads it: an entity's summary, or a
// statement as the knower holds it.
type SearchHit struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Snippet is prose around the match. A statement's prose states canon
	// truth, so a scoped read leaves it out.
	Snippet   string           `json:"snippet,omitempty"`
	Rank      float64          `json:"rank"`
	Entity    *resolve.Summary `json:"entity,omitempty"`
	Statement *resolve.Belief  `json:"statement,omitempty"`
}

// Search hit kinds, as the index records them.
const (
	kindEntity    = "entity"
	kindStatement = "statement"
)

// EntityResult is one entity under the context.
type EntityResult struct {
	resolve.Entity
	// Source is the entity's file as authored, frontmatter and prose, so an
	// agent can see the file format before it proposes one. Only an author
	// read without a knower has it: the file states canon truth.
	Source string `json:"source,omitempty"`
	Report
}

// GraphResult is a neighbourhood under the context.
type GraphResult struct {
	resolve.Graph
	Report
}

// BeliefsResult is what one agent believes under the context.
type BeliefsResult struct {
	Agent   string           `json:"agent"`
	Beliefs []resolve.Belief `json:"beliefs"`
	Report
}

// DiffResult is what two worldlines disagree on.
type DiffResult struct {
	resolve.Diff
	Report
}

// reported is a result that carries the build report.
type reported[T any] interface {
	*T
	setReport(Report)
}

// addRead registers a read tool. The handler runs against the current build,
// reloaded first if the world changed, and its result carries the report of a
// failed build.
func addRead[In, Out any, P reported[Out]](s *Server, tool *sdk.Tool, fn func(cur *loaded, in In) (Out, error)) {
	sdk.AddTool(s.mcp, tool, func(_ context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		var out Out
		report, err := s.read(func(cur *loaded) error {
			var err error
			out, err = fn(cur, in)
			return err
		})
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		P(&out).setReport(report)
		return nil, out, nil
	})
}

// register adds every tool. Each read builds its context through
// readContext and reads only through the resolver.
func (s *Server) register() {
	addRead(s, &sdk.Tool{
		Name:        "get_world_index",
		Description: "List the entities present under the context, grouped by type, with every decision and its outcomes, the eras, and the relation vocabulary.",
	}, s.worldIndex)
	addRead(s, &sdk.Tool{
		Name:        "search",
		Description: "Full-text search over names, aliases and prose. Only hits present under the context are returned.",
	}, s.search)
	addRead(s, &sdk.Tool{
		Name:        "get_entity",
		Description: "Read one entity under the context: its edges, and the statements about it as canon or the knower has them.",
	}, s.entity)
	addRead(s, &sdk.Tool{
		Name:        "expand",
		Description: "Walk the graph from an entity under the context, up to depth hops, optionally along named relations only.",
	}, s.expand)
	addRead(s, &sdk.Tool{
		Name:        "get_beliefs",
		Description: "List what an agent believes under the context. With a knower, only the knower's own beliefs can be read.",
	}, s.beliefs)
	addRead(s, &sdk.Tool{
		Name:        "diff_worldlines",
		Description: "Compare two worldlines: the authored facts present only under a, and only under b.",
	}, s.diff)

	sdk.AddTool(s.mcp, &sdk.Tool{
		Name:        "validate",
		Description: "Check proposed entity and statement files against the world without writing anything.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in validateArgs) (*sdk.CallToolResult, ValidateResult, error) {
		out, err := s.validate(ctx, in.Files)
		return nil, out, err
	})
	sdk.AddTool(s.mcp, &sdk.Tool{
		Name:        "propose_change",
		Description: "Write proposed entity and statement files as a proposal for a human to review. Nothing in the world changes.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in proposeArgs) (*sdk.CallToolResult, ProposeResult, error) {
		out, err := s.propose(ctx, in.Files, in.Summary)
		return nil, out, err
	})
}

func (s *Server) worldIndex(cur *loaded, in worldIndexArgs) (WorldIndex, error) {
	ctx, err := s.readContext(in.Worldline, in.Knower, in.Statuses)
	if err != nil {
		return WorldIndex{}, err
	}
	ents, err := cur.res.Entities(ctx)
	if err != nil {
		return WorldIndex{}, err
	}
	out := WorldIndex{Types: []TypeGroup{}, Decisions: []Decision{}, Relations: []RelationInfo{}}
	byType := map[string][]resolve.Summary{}
	for _, e := range ents {
		byType[e.Type] = append(byType[e.Type], e)
	}
	for _, t := range slices.Sorted(maps.Keys(byType)) {
		out.Types = append(out.Types, TypeGroup{Type: t, Entities: byType[t]})
	}

	// Decisions are read omnisciently, when the role allows, under the same
	// worldline and statuses: a caller learns which decisions exist before
	// choosing outcomes for them.
	dctx := ctx
	if s.pol.omniscient {
		opts, err := s.options(in.Statuses)
		if err != nil {
			return WorldIndex{}, err
		}
		dctx = resolve.Omniscient(resolve.Worldline(in.Worldline), opts...)
	}
	decs, err := cur.res.Entities(dctx)
	if err != nil {
		return WorldIndex{}, err
	}
	for _, d := range decs {
		if d.Type != schema.TypeDecision {
			continue
		}
		e, err := cur.res.Entity(dctx, d.ID)
		if err != nil {
			return WorldIndex{}, err
		}
		out.Decisions = append(out.Decisions, Decision{ID: e.ID, Name: e.Name, Outcomes: e.Outcomes})
	}

	out.Eras = slices.Clone(cur.idx.Vocabulary.Eras)
	if out.Eras == nil {
		out.Eras = []index.Ordered{}
	}
	for _, r := range cur.idx.Vocabulary.Relations {
		out.Relations = append(out.Relations, RelationInfo{Name: r.Name, Inverse: r.Inverse, Symmetric: r.Symmetric, Role: r.Role})
	}
	return out, nil
}

// search finds hits in build/index.db and re-reads each one through the
// resolver. The index knows nothing of worldlines, knowers or statuses, so a
// hit the resolver does not return is dropped.
func (s *Server) search(cur *loaded, in searchArgs) (SearchResult, error) {
	ctx, err := s.readContext(in.Worldline, in.Knower, in.Statuses)
	if err != nil {
		return SearchResult{}, err
	}
	// Listing the present entities checks the context first, so a bad
	// knower or worldline is an error rather than an empty result.
	ents, err := cur.res.Entities(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	present := make(map[string]bool, len(ents))
	for _, e := range ents {
		present[e.ID] = true
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)

	hits, err := index.Search(cur.db, in.Query, maxSearchLimit)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Hits: []SearchHit{}}
	for _, h := range hits {
		if len(out.Hits) == limit {
			break
		}
		hit := SearchHit{ID: h.ID, Kind: h.Kind, Snippet: h.Snippet, Rank: h.Rank}
		switch h.Kind {
		case kindEntity:
			if !present[h.ID] {
				continue
			}
			e, err := cur.res.Entity(ctx, h.ID)
			if err != nil {
				return SearchResult{}, err
			}
			hit.Entity = &e.Summary
		case kindStatement:
			subject, ok := cur.subjects[h.ID]
			if !ok || !present[subject] {
				continue
			}
			e, err := cur.res.Entity(ctx, subject)
			if err != nil {
				return SearchResult{}, err
			}
			i := slices.IndexFunc(e.Statements, func(b resolve.Belief) bool { return b.Statement == h.ID })
			if i < 0 {
				continue // absent, or the knower is ignorant of it
			}
			hit.Statement = &e.Statements[i]
			if !ctx.IsOmniscient() {
				hit.Snippet = ""
			}
		default:
			continue
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

func (s *Server) entity(cur *loaded, in entityArgs) (EntityResult, error) {
	ctx, err := s.readContext(in.Worldline, in.Knower, in.Statuses)
	if err != nil {
		return EntityResult{}, err
	}
	e, err := cur.res.Entity(ctx, in.ID)
	if err != nil {
		return EntityResult{}, err
	}
	out := EntityResult{Entity: e}
	if ctx.IsOmniscient() {
		// A file gone since the last good build leaves source out; the
		// result's report says that build is stale.
		src, err := os.ReadFile(filepath.Join(s.repo, index.WorldDir, filepath.FromSlash(e.File)))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return EntityResult{}, fmt.Errorf("reading the file of %s: %w", e.ID, err)
		}
		out.Source = string(src)
	}
	return out, nil
}

func (s *Server) expand(cur *loaded, in expandArgs) (GraphResult, error) {
	ctx, err := s.readContext(in.Worldline, in.Knower, in.Statuses)
	if err != nil {
		return GraphResult{}, err
	}
	g, err := cur.res.Expand(ctx, in.ID, in.Depth, in.Relations)
	return GraphResult{Graph: g}, err
}

func (s *Server) beliefs(cur *loaded, in beliefsArgs) (BeliefsResult, error) {
	ctx, err := s.readContext(in.Worldline, in.Knower, in.Statuses)
	if err != nil {
		return BeliefsResult{}, err
	}
	bs, err := cur.res.Beliefs(ctx, in.Agent)
	return BeliefsResult{Agent: in.Agent, Beliefs: bs}, err
}

// diff builds each side's context from the session role and that side's
// worldline, with the same knower and statuses.
func (s *Server) diff(cur *loaded, in diffArgs) (DiffResult, error) {
	a, err := s.readContext(in.A, in.Knower, in.Statuses)
	if err != nil {
		return DiffResult{}, err
	}
	b, err := s.readContext(in.B, in.Knower, in.Statuses)
	if err != nil {
		return DiffResult{}, err
	}
	d, err := cur.res.Diff(a, b)
	return DiffResult{Diff: d}, err
}
