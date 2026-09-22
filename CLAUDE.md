# lore-core

Tooling for a graph-based lore system for fictional worlds: a CLI, a validator, an index
builder, a query resolver, an MCP server, and a web editor. Authored lore lives in a
separate project repo as Markdown + YAML frontmatter; this repo is installed there as a
versioned Go module.

Full design: `docs/spec.md`. Read the relevant section before changing behaviour — this
file lists rules, not reasoning.

## Current phase

Phase 1: schema pack format, validator, index builder. Steps and acceptance criteria are
in the Implementation plan section of the spec.

## Commands

```
go run ./cmd/lore build <world-dir>   # validate + emit index and snapshot
go test ./...                         # all tests
go test ./... -update                 # regenerate testdata/ goldens
```

## Invariants

These hold everywhere. A change that violates one is wrong even if it passes tests.

- **Entity IDs are stable and opaque.** Never derived from names, titles, or filenames.
  Nothing renames an ID.
- **Frontmatter is the only graph source.** Nothing is ever parsed out of prose.
- **The relation vocabulary is closed.** Unknown relation or entity types fail the build.
  Relations come from the schema packs, never from a literal in code — a relation name
  hardcoded anywhere breaks reuse across projects. Graph views bind to `role` tags
  (`containment`, `membership`), not to relation names.
- **Every read goes through the resolver**, with its three context parameters: worldline,
  knower, visibility. Unscoped canon access requires an explicit flag; the awkward path is
  the omniscient one.
- **The query context comes from the session role on the server.** Never from UI state or
  a client-supplied parameter.
- **Agents propose, never write canon.** `propose_change` produces a draft commit or PR
  for a human to promote.
- **The derived index is disposable.** It is rebuilt from the repo and never edited in
  place; the files are the source of truth.
- **Story branches are data** (`valid_in` conditions), never git branches.
- **Statements are for contested facts only.** Plain typed edges by default; reify only
  when a character needs to be wrong.

## Where things go

`lore-core` holds anything whose fix must reach existing projects. Project-specific
vocabulary and content belong in the project repo. When unsure, ask rather than guess —
putting something in the wrong place is expensive to undo later.

```
cmd/lore/          CLI entrypoint
internal/schema/   schema pack parsing, core pack
internal/index/    build pipeline, SQLite index, snapshot export
internal/resolve/  query resolver
internal/mcp/      MCP server
internal/editor/   web editor (templ + htmx)
docs/spec.md       the design spec
```

## Workflow

For changes to `internal/schema`, `internal/resolve`, or validation rules: write a plan
first and wait for approval, then tests from the acceptance criteria, then implementation.
These are the places where a subtle error is invisible — a broken visibility filter leaks
spoilers rather than crashing, and no test you didn't think to write will catch it.

Everything else: implement directly.

Never claim something works without running it. Golden diffs get read, not accepted.

## Commits

As short as possible, yet a reader knows every effect without opening the diff.

```
Add the validator

- add 18 blocking checks, 5 warnings
- add CI gate: referential integrity only
- add id rule: [a-z0-9_]
```

- **Summary:** one line, 50 characters maximum. Capitalised, imperative, no trailing
  period, no scope prefix.
- **Body:** a blank line, then one bullet per observable effect (a behaviour, an API,
  a rule), not one per file. Lowercase, 72 characters per line.
- **Every bullet starts with a verb from this closed set:**
  - `add X`
  - `remove X`
  - `update X: old → new`
  - `fix <symptom>`: what was broken, not how it was fixed
  - `move X → path`
  - `rename a → b`
- **Nouns are identifiers and paths** (`internal/index`, `Interval`), not prose.
- **Facts only, no reasoning.** The *why* belongs in doc comments and `docs/spec.md`;
  if it is not there, it is nowhere.
- **Omit the body** when the summary is the whole change.
- **No line cap.** Too many bullets means the commit should be split.
- **One logical change per commit**, and each commit builds and tests green on its own.
- **Reverts and merges** keep git's default message.
- **No Claude attribution.** No `Co-Authored-By` trailer, and no "Generated with"
  line in PR descriptions.

## Constraints

- Go only. The one exception is the editor's browser UI: templ and htmx, with a
  JavaScript island permitted for graph projections in Step 8 and nowhere else.
- Dependencies stay few: `gopkg.in/yaml.v3`, `modernc.org/sqlite` (pure Go, no cgo),
  `go-git`, the MCP SDK, `go-cmp` in tests. Adding one is a decision to raise, not make.
- **Windows is the primary platform** for every developer and writer:
  - `filepath` for paths, never string concatenation
  - IDs and filenames must not collide case-insensitively — the validator enforces this
  - No Make, no shell scripts in the build path
  - Anything writers run must work from a single `.exe` with nothing else installed
