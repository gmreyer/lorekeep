# lore-core

Tooling for a graph-based lore system for fictional worlds: a CLI, a validator, an
index builder, a query resolver, an MCP server, and a web editor.

Authored lore does not live here. It lives in a **world repo** as Markdown with YAML
frontmatter, and this module is installed there as a versioned dependency. The split
is deliberate: schema packs promote upstream, so a relation that proves general in one
world moves into the core pack in a later release and every world picks it up by
bumping a version. A copied template could never receive those updates.

The full design is in [docs/spec.md](docs/spec.md).

## Status

Phase 1, Step 1 complete. What exists today:

- `internal/schema` — the schema pack format, its Go types, a loader, the
  project-over-core merge, and the embedded core pack: 7 entity types, 8 roles,
  14 structural relations, plus a project pack's eras and spoiler acts
- `internal/world` — the authored file format: entities and statements as Markdown
  with YAML frontmatter, a parser that locates every field by line, and a loader
- `internal/validate` — the blocking-error list and the warnings
- `internal/index` — the SQLite index and the JSON game snapshot
- `cmd/lore` — `lore build <world-dir>`

Not built yet: the query resolver, the MCP server, and the editor. Belief
inheritance, worldline resolution, health metrics, and the advisory AI lane are
deferred to later steps; beliefs, statements and `valid_in` are parsed, carried,
and checked for referential integrity now, so the file format is final.

No release tags have been cut, so there is no version for a world repo to pin.

## Working on lore-core

```
go test ./...
go test ./... -update    # regenerate the goldens under internal/index/testdata/
go build ./...
go run ./cmd/lore build testdata/world-ok
```

`testdata/world-ok/` is the one fixture world in this repo — a schema pack beside
authored content — and the validator, the index builder, and the CLI all read it.
One copy means one place where the format and the vocabulary have to agree.

The index golden is a **text dump** of the database rather than the `.db` file. A
SQLite file's bytes depend on page allocation, so a binary golden could only ever
be accepted, never read; the rule is that golden diffs get read. Regeneration
stays an explicit flag for the same reason — a diff that looks cosmetic may be a
visibility field silently changing kind.

Go 1.25 or newer. Three dependencies: `gopkg.in/yaml.v3`, `modernc.org/sqlite`
(pure Go, no cgo, FTS5 included), and `go-cmp` in tests. `go-git` and the MCP SDK
arrive with the steps that need them. Adding a dependency is a decision to raise,
not to make.

## Using it from a world repo

A world repo requires this module in its `go.mod` and pins a version by git tag:

```
require github.com/gmreyer/lore-core v0.1.0
```

**This repo is private, so set `GOPRIVATE` once, on every machine, before your first
`go get`:**

```bash
go env -w GOPRIVATE=github.com/gmreyer/*
```

Without it the Go toolchain tries the public module proxy, gets a 404, and reports
something that looks nothing like a permissions problem. This is the single thing
most likely to cost someone an afternoon on day one.

A world repo also carries a `core_version` pin in its schema pack, recording which
core vocabulary the prose was authored against. That is not the same fact as the
`go.mod` requirement — `go.mod` says which binary you run, `core_version` says what
the writing assumed. The loader compares them and says so when they drift.

## Windows notes

Windows is the primary platform for every developer and writer here, which
constrains the pipeline more than it constrains the code.

- **Keep clones out of OneDrive.** Windows points `Documents` at OneDrive by
  default, and a synced git repo produces file locks and phantom conflicts that read
  as tooling bugs. Clone wherever Explorer opens instead — `C:\work`, `A:\`, anywhere
  outside the sync root.
- **Line endings** are normalised to LF by `.gitattributes`. World files are the
  source of truth and review happens in diffs, so CRLF churn would put every line of
  every file into every diff.
- **Long paths**, precautionary, since the layout is flat:
  ```bash
  git config --global core.longpaths true
  ```
- **IDs and filenames must not collide case-insensitively.** Windows treats
  `Kaelen-Vor.md` and `kaelen-vor.md` as one file and git does not, so a pair that
  works for one writer collides for another. The validator makes this a blocking
  error.
- No Make and no shell scripts in the build path. Anything a writer runs must work
  from a single `.exe` with nothing else installed.

## Layout

```
cmd/lore/          CLI entrypoint
internal/schema/   schema pack parsing, core pack
internal/world/    the authored file format: types, frontmatter parser, loader
internal/validate/ blocking errors and warnings
internal/index/    build pipeline, SQLite index, snapshot export
internal/resolve/  query resolver (not built yet)
internal/mcp/      MCP server (not built yet)
internal/editor/   web editor, templ + htmx (not built yet)
testdata/world-ok/ the fixture world, shared by every consumer
docs/spec.md       the design spec
```

## Building a world

```
lore build <world-dir> [-out <dir>]
```

A world directory holds a schema pack in `schema/` and authored lore in `world/`.
`build` validates the whole repository and, if it is sound, writes `index.db` and
`snapshot.json` into `build/`. **Nothing is written when validation fails** — a
half-built index of a world with a dangling reference is worse than no index,
because it would answer questions and the answers would be wrong.

Blocking errors are referential integrity only: dangling ids, unknown relation
or entity types or eras, domain and range violations, an authored inverse name,
a condition naming an outcome that does not exist, a belief pointing at a
non-statement, duplicate ids, case-insensitive collisions, ids outside the
permitted charset, unknown spoiler acts, the same edge authored twice under the
same conditions, and a symmetric edge authored on both of its endpoints.

Everything judgemental is a warning and never blocks: a character at an event
outside their lifespan, an orphan entity, canon depending on a draft, a statement
nobody believes, and a directory that disagrees with a file's type. A check that
is right most of the time must not be a gate, or the team learns to bypass the
build.
