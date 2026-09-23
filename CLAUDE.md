# lorekeep

Tooling for a graph-based lore system for fictional worlds: a CLI, a validator, an index
builder, a query resolver, an MCP server, and a web editor. Authored lore lives in a
separate project folder as Markdown + YAML frontmatter, created by `lorekeep setup`;
the project pins a lorekeep release in `lorekeep-version`, and lorekeep runs that
release for it. Writers use `lorekeep.exe` only, never Go.

The design lives outside the repo, in two Claude Docs read through the Claude Docs
connector:

- [Lore Graph — System Design Spec](https://claude.ai/artifact/Rbpb6TD77aZXTVK9YKUnPi):
  the full design. Read the relevant section before changing behaviour — this file lists
  rules, not reasoning.
- [Lore Graph — Implementation Status](https://claude.ai/artifact/6f7YChDHeKa8LNuaMUwyCY):
  what of the spec is built.

Update either doc only when a real change lands, and only after review: show the user the
exact proposed edit to the spec or status doc, ask explicitly for approval, and write it
only on a clear yes. Never edit either doc as a side effect of other work.

## Current phase

Phase 1: schema pack format, validator, index builder. Steps and acceptance criteria are
in the Implementation plan section of the spec.

## Commands

```
go run ./cmd/lorekeep build <world-dir>                      # validate + emit index and snapshot
go run ./cmd/lorekeep setup <dir> -name <n> -version v0.3.0  # a scratch project
go test ./...                                                # all tests
go test ./... -update                                        # regenerate testdata/ goldens
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

`lorekeep` holds anything whose fix must reach existing projects. Project-specific
vocabulary and content belong in the project. When unsure, ask rather than guess —
putting something in the wrong place is expensive to undo later.

```
cmd/lorekeep/        CLI entrypoint, menu, version dispatch
internal/schema/     schema pack parsing, core pack
internal/world/      authored file format, parser, loader
internal/validate/   blocking errors and warnings
internal/index/      build pipeline, SQLite index, snapshot export
internal/scaffold/   the embedded project template
internal/project/    lorekeep-version, recent projects
internal/versions/   release cache, downloads, update check
internal/resolve/    query resolver
internal/mcp/        MCP server
internal/editor/     web editor (templ + htmx)
```

## Projects, versions and the template

A writer's whole toolchain is `lorekeep.exe`: the menu, `setup`, `build`, `update`,
`git`. These rules hold for all of it.

- **Git is optional.** lorekeep must work for a purely local project and never runs
  git. The layout stays git-friendly regardless: plain text, one entity per file,
  derived output only in `build/`. Git files and the CI workflow are opt-in, at
  setup or later through `lorekeep git`. The spec's editor commits and
  `propose_change` assume a repo; how they work locally is open for Steps 5–6.
- **The user never edits config by hand.** Anything user-specific comes through a
  prompt or a flag, and lorekeep writes the file.
- **The pin format is frozen.** `lorekeep-version` is one line, `vX.Y.Z`. Old
  binaries read it to dispatch to new ones, so nothing is ever added to it.
- **Commands on a project go through `runPinned`,** so they run the release the
  project pins. `LOREKEEP_NO_DISPATCH` marks a child lorekeep started from the cache;
  it never dispatches again.
- **Nothing silent.** Downloads and updates are asked for, at a real console only
  (`isTerminal`, not a file mode: `NUL` is a character device). Without a console
  lorekeep never prompts, never downloads, and records no answer nobody gave. A
  project's pin moves only after the new release builds it with exit 0.
- **The template is `internal/scaffold/files/`.** `base/` is always written;
  `example/`, `git/` and `ci/` on request. Dotfiles are stored under inert names and
  mapped on write, so they never act on this repo. Changing the template changes
  only projects created afterwards; a fix existing projects need belongs in the
  tool. The scaffolded README is for writers and is never updated after setup, so it
  says nothing that a later release could make false.
- **The example world** shows a writer the format; it does not test the validator,
  which is `testdata/world-ok/`. Every setup variant must build with zero findings.
- **The CI workflow** downloads the pinned release and checks it against
  `SHA256SUMS`. When its script changes, run it against a real release before
  merging; unit tests do not execute PowerShell.
- **`lore-project-template`** is retired. Nothing new goes there.

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
- **Facts only, no reasoning.** The *why* belongs in doc comments and the spec;
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
