# lorekeep

Tooling for a graph-based lore system for fictional worlds: a CLI, a validator, an
index builder, a query resolver, an MCP server, and a web editor.

Authored lore does not live here. It lives in a **world repo** as Markdown with YAML
frontmatter, and this module is installed there as a versioned dependency. The split
is deliberate: schema packs promote upstream, so a relation that proves general in one
world moves into the core pack in a later release and every world picks it up by
bumping a version. A copied template could never receive those updates.

The full design is in
[Lore Graph — System Design Spec](https://claude.ai/artifact/Rbpb6TD77aZXTVK9YKUnPi), and
what of it is built is in
[Lore Graph — Implementation Status](https://claude.ai/artifact/6f7YChDHeKa8LNuaMUwyCY).
Both are Claude Docs, private until shared.

## Status

Phase 1, Step 1 complete. What exists today:

- `internal/schema` — the schema pack format, its Go types, a loader, the
  project-over-core merge, and the embedded core pack: 7 entity types, 8 roles,
  14 structural relations, plus a project pack's eras and spoiler acts
- `internal/world` — the authored file format: entities and statements as Markdown
  with YAML frontmatter, a parser that locates every field by line, and a loader
- `internal/validate` — the blocking-error list and the warnings
- `internal/index` — the SQLite index and the JSON game snapshot
- `cmd/lorekeep` — `lorekeep build <world-dir>`

Not built yet: the query resolver, the MCP server, and the editor. Belief
inheritance, worldline resolution, health metrics, and the advisory AI lane are
deferred to later steps; beliefs, statements and `valid_in` are parsed, carried,
and checked for referential integrity now, so the file format is final.

Released: `v0.1.0`. Step 2, the world-repo template, is
[lore-project-template](https://github.com/gmreyer/lore-project-template).

## Working on lorekeep

```
go test ./...
go test ./... -update    # regenerate the goldens under internal/index/testdata/
go build ./...
go run ./cmd/lorekeep build testdata/world-ok
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

Start a new world from
[lore-project-template](https://github.com/gmreyer/lore-project-template) rather than
by hand: it carries the directory skeleton, an empty project pack, a fixture world,
and a CI workflow that builds the world on every push.

A world repo requires this module in its `go.mod`, pins a version by git tag, and
declares the CLI as a tool (Go 1.24 or newer):

```
go 1.25.0

require github.com/gmreyer/lorekeep v0.2.1

tool github.com/gmreyer/lorekeep/cmd/lorekeep
```

The `tool` directive means nobody installs `lorekeep` separately. From the world repo's
root, the pinned version builds and runs on demand:

```
go tool lorekeep build .
```

Upgrading lorekeep is `go get github.com/gmreyer/lorekeep@v0.X.Y` in the world repo,
then a commit of the changed `go.mod` and `go.sum`.

**This repo is private, so set `GOPRIVATE` once, on every machine, before your first
`go get`:**

```bash
go env -w "GOPRIVATE=github.com/gmreyer/*"
```

Without it the Go toolchain tries the public module proxy, gets a 404, and reports
something that looks nothing like a permissions problem. This is the single thing
most likely to cost someone an afternoon on day one.

Go fetches the module over HTTPS with git's password prompts switched off, so an SSH
key does not help. What works on Windows is the GitHub CLI signed in over HTTPS,
answering yes to "authenticate Git with your GitHub credentials":

```bash
gh auth login
```

In CI there is no signed-in user, so the template's workflow rewrites
`https://github.com/gmreyer/` to carry a fine-grained token with read access to this
repo. When that token expires the build fails as a module download error, not as an
authentication error — check the token first.

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

## Getting lorekeep.exe without Go

Writers who do not have Go installed run a prebuilt `lorekeep.exe` instead of
`go tool lorekeep`. Each version tag, from v0.2.1 on, has a release on this repo's
[Releases page](https://github.com/gmreyer/lorekeep/releases) with `lorekeep.exe`
(windows/amd64, no cgo) and `SHA256SUMS` attached. This repo is private, so
downloading needs a GitHub account with read access to it.
v0.2.0 was released as `github.com/gmreyer/lore-core` and ships `lore.exe`; the
first release under the `lorekeep` name is v0.2.1.

Download the release matching the version in your world repo's `go.mod`, check it,
and confirm the version:

```powershell
(Get-FileHash lorekeep.exe -Algorithm SHA256).Hash.ToLower()   # compare with SHA256SUMS
.\lorekeep.exe version
```

The binary is not code-signed, so on first run Windows SmartScreen may say it
"protected your PC". Choose **More info → Run anyway**, having checked the hash
first.

Releases are cut by pushing a `vX.Y.Z` tag. `.github/workflows/release.yml` tests,
builds, checks that the binary reports the tag, and creates a **draft** release.
A person writes what the release breaks into its notes and publishes it.

## Layout

```
cmd/lorekeep/      CLI entrypoint
internal/schema/   schema pack parsing, core pack
internal/world/    the authored file format: types, frontmatter parser, loader
internal/validate/ blocking errors and warnings
internal/index/    build pipeline, SQLite index, snapshot export
internal/resolve/  query resolver (not built yet)
internal/mcp/      MCP server (not built yet)
internal/editor/   web editor, templ + htmx (not built yet)
testdata/world-ok/ the fixture world, shared by every consumer
```

## Building a world

```
lorekeep build <world-dir> [-out <dir>]
```

Inside a world repo that is `go tool lorekeep build .`.

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
