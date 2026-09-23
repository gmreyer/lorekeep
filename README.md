# lorekeep

Tooling for a graph-based lore system for fictional worlds: a CLI, a validator, an
index builder, a query resolver, an MCP server, and a web editor.

Authored lore does not live here. It lives in a **project**: a folder of Markdown
files with YAML frontmatter, created by `lorekeep setup`. The tooling is never copied
into a project. Instead each project pins a lorekeep release in its `lorekeep-version`
file, and lorekeep runs that release for it. The split is deliberate: schema packs
promote upstream, so a relation that proves general in one world moves into the core
pack in a later release, and every project picks it up by moving its pin.

Writers need nothing but `lorekeep.exe`: no Go, no git, no account.

The full design is in
[Lore Graph — System Design Spec](https://claude.ai/artifact/Rbpb6TD77aZXTVK9YKUnPi), and
what of it is built is in
[Lore Graph — Implementation Status](https://claude.ai/artifact/6f7YChDHeKa8LNuaMUwyCY).
Both are Claude Docs, private until shared.

## Status

Phase 1. What exists today:

- `internal/schema`: the schema pack format, its Go types, a loader, the
  project-over-core merge, and the embedded core pack: 7 entity types, 8 roles,
  14 structural relations, plus a project pack's eras and spoiler acts
- `internal/world`: the authored file format: entities and statements as Markdown
  with YAML frontmatter, a parser that locates every field by line, and a loader
- `internal/validate`: the blocking-error list and the warnings
- `internal/index`: the SQLite index and the JSON game snapshot
- `internal/scaffold`: the embedded project template
- `internal/project`: the `lorekeep-version` pin and the recent-projects list
- `internal/versions`: the per-user cache of lorekeep releases, verified downloads,
  and the update check
- `cmd/lorekeep`: the menu and the `build`, `setup`, `update`, `git` and `version`
  commands

Not built yet: the query resolver, the MCP server, and the editor. Belief
inheritance, worldline resolution, health metrics, and the advisory AI lane are
deferred to later steps. Beliefs, statements and `valid_in` are already parsed,
carried, and checked for referential integrity, so the file format is final.

## Getting lorekeep.exe

Download `lorekeep.exe` and `SHA256SUMS` from the
[Releases page](https://github.com/gmreyer/lorekeep/releases). Releases are
windows/amd64 with no cgo, and need nothing else installed. Put the exe in any folder
you like, then check it:

```powershell
(Get-FileHash lorekeep.exe -Algorithm SHA256).Hash.ToLower()   # compare with SHA256SUMS
.\lorekeep.exe version
```

The binary is not code-signed, so on first run Windows SmartScreen may say it
"protected your PC". Check the hash first, then choose **More info → Run anyway**.

v0.2.0 and earlier were released as `lore-core` with `lore.exe`. Projects need
v0.3.0 or later, the first release that reads `lorekeep-version`.

## The menu

Double-click `lorekeep.exe`, or run it with no arguments in a PowerShell or cmd
window, and it shows a numbered menu:

- with no project open: **Set up a new project**, **Open a project**
- with a project open: **Build**, **Update lorekeep for this project**, then
  **Prepare for git** or **Add a GitHub CI workflow** when those files are missing,
  **Open another project**, **Set up a new project**

The menu opens the project in the current folder if there is one, otherwise the last
project you opened. Every item runs the same command you could type (below), so the
menu and the commands always behave the same way. The web editor replaces the menu
once it exists.

**Setting up** asks for an empty or new folder, a project name (a suggestion is made
from the folder name), and three yes/no questions: a small example world, git files,
and a GitHub CI workflow. Every answer defaults to no, so pressing Enter all the way
through gives a plain local project.

## Commands

```
lorekeep                                  the menu, at a console
lorekeep build <world-dir> [-out <dir>]
lorekeep setup <dir> -name <name> [-example] [-git] [-ci] [-version <vX.Y.Z>]
lorekeep update [<world-dir>] [-version <vX.Y.Z>]
lorekeep git [<world-dir>] [-ci]
lorekeep version
```

Flags can come before or after the directory. Every command exits:

- `0`: it worked.
- `1`: `build` found blocking errors and wrote nothing, or `update` found that the
  new version reports errors and left the pin alone.
- `2`: bad invocation, or something outside the world stopped it: no `schema/` or
  `world/`, an unreadable pin, a version that could not be downloaded.

- **`build`** validates the project and, if it is sound, writes `build/index.db` and
  `build/snapshot.json`. See [Building a world](#building-a-world).
- **`setup`** creates a project in an empty or new folder, pinned to the running
  lorekeep. It writes `schema/` with an empty project pack, `world/`,
  `lorekeep-version`, and a README for writers. `-example` adds a five-entity example
  world, `-git` adds the git files, and `-ci` adds the workflow (it needs `-git`). It
  never overwrites anything, and removes what it wrote if it fails part-way.
- **`update`** moves the project to the latest release, or to `-version`. See
  [Versions and updates](#versions-and-updates).
- **`git`** adds the git files, and the workflow with `-ci`, to an existing project.
  See [Local or git](#local-or-git).
- **`version`** prints `lorekeep vX.Y.Z`.

## Versions and updates

A project's `lorekeep-version` file holds one line, such as `v0.3.0`. That is the
lorekeep release that builds it. Two versions are pinned, and they record different
facts: `lorekeep-version` says which binary runs, and `core_version` in
`schema/pack.yaml` says which core vocabulary the lore was written against. `build`
reports when they drift.

- **The pinned version runs.** When `build` or `git` runs on a project pinned to a
  different release, lorekeep runs that release instead, with the same arguments,
  and returns its exit code. Releases are kept side by side in
  `%LOCALAPPDATA%\lorekeep\versions\`, so projects on different pins coexist.
- **Downloads are asked for.** If the pinned release is not on this machine, lorekeep
  asks whether to download it. Every download is checked against the release's
  `SHA256SUMS`, and a failed or tampered one leaves nothing behind. Without a
  console, as in CI or a script, lorekeep never asks and never downloads: it exits 2.
- **Updates are never silent.** At a console, `build` checks for a newer release at
  most once a day, shows its release notes, and asks
  *Update this project? [y/N]*. A no is remembered for that release. Offline, it says
  nothing.
- **A project moves only if it still builds.** Whether you accept the offer or run
  `lorekeep update`, the new release builds the project first. The pin moves only if
  that build is clean; otherwise its findings are shown and the project stays where it
  was. Read the release notes: a new release can add blocking errors. `update` never
  moves to an older release unless `-version` asks for one.

## Local or git

A project works without git, and lorekeep never runs git itself. The layout still
suits git well: plain text, one entity per file, derived output only in `build/`.
That way the git files can be added at any time without moving anything.

`setup -git` or `lorekeep git` adds:

- `.gitattributes`: line endings stay LF, so a diff shows only real changes
- `.gitignore`: `build/` and `*.exe` stay out of the repository
- `world/.gitkeep`: git stores no empty folders, and `build` needs `world/`

`-ci` also adds `.github/workflows/build.yml`. On every push and pull request it
downloads the pinned `lorekeep.exe`, checks it against `SHA256SUMS`, and runs
`lorekeep build .` on Windows. Any blocking error fails the run. It needs no secrets.

`lorekeep git` writes only files that are missing. If a file exists and differs from
lorekeep's copy, it is yours: at a console lorekeep asks before replacing it, and
otherwise keeps it.

## Where lorekeep keeps its own files

Outside any project, per user, and safe to delete:

- `%LOCALAPPDATA%\lorekeep\versions\`: downloaded releases
- `%APPDATA%\lorekeep\state.json`: the last update check, and releases you declined
- `%APPDATA%\lorekeep\projects.json`: recently opened projects

## Building a world

A world directory holds a schema pack in `schema/` and authored lore in `world/`.
`build` validates the whole repository and, if it is sound, writes `index.db` and
`snapshot.json` into `build/`. **Nothing is written when validation fails.** A
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

## Windows notes

Windows is the primary platform for every developer and writer here, which
constrains the pipeline more than it constrains the code.

- **Prompts need a console.** A PowerShell or cmd window, or a double-clicked
  `lorekeep.exe`, can answer lorekeep's questions. Git Bash's terminal (mintty) is
  not a Windows console, so lorekeep sees nobody to ask there and behaves as it does
  in CI.
- **Keep projects out of OneDrive.** Windows points `Documents` at OneDrive by
  default, and a synced folder produces file locks and phantom conflicts that read as
  tooling bugs, all the more so under git. Use `C:\work`, `A:\`, anywhere outside the
  sync root.
- **IDs and filenames must not collide case-insensitively.** Windows treats
  `Kaelen-Vor.md` and `kaelen-vor.md` as one file and git does not, so a pair that
  works for one writer collides for another. The validator makes this a blocking
  error.
- **Long paths**, precautionary for git users, since the layout is flat:
  ```bash
  git config --global core.longpaths true
  ```
- No Make and no shell scripts in the build path. Anything a writer runs must work
  from a single `.exe` with nothing else installed.

## Working on lorekeep

```
go test ./...
go test ./... -update    # regenerate the goldens under internal/index/testdata/
go build ./...
go run ./cmd/lorekeep build testdata/world-ok
go run ./cmd/lorekeep setup <dir> -name <name> -version v0.3.0
```

Go 1.25 or newer. Three dependencies: `gopkg.in/yaml.v3`, `modernc.org/sqlite`
(pure Go, no cgo, FTS5 included), and `go-cmp` in tests. `go-git` and the MCP SDK
arrive with the steps that need them. Adding a dependency is a decision to raise,
not to make.

`testdata/world-ok/` is the one fixture world for the validator, the index builder,
and the CLI. One copy means one place where the format and the vocabulary have to
agree. The example world in `internal/scaffold/files/example/` is different: it shows
a writer the format, and its only test is that it builds with no findings at all.

The index golden is a **text dump** of the database rather than the `.db` file. A
SQLite file's bytes depend on page allocation, so a binary golden could only ever
be accepted, never read, and the rule is that golden diffs get read. Regeneration
stays an explicit flag for the same reason: a diff that looks cosmetic may be a
visibility field silently changing kind.

A development build (`go run`, or `go build` from a checkout) is not a release. It
ignores a project's pin and says so, and `setup` needs `-version` to know what to
pin. lorekeep sets `LOREKEEP_NO_DISPATCH=1` for any release it runs from the cache;
set it yourself to make any build run as itself.

Releases are cut by pushing a `vX.Y.Z` tag. `.github/workflows/release.yml` tests,
builds `lorekeep.exe`, checks that it reports the tag, and creates a **draft**
release with `SHA256SUMS`. A person writes what the release breaks into its notes and
publishes it. Those notes are what the update offer shows writers.

## Layout

```
cmd/lorekeep/        CLI entrypoint, menu, version dispatch
internal/schema/     schema pack parsing, core pack
internal/world/      the authored file format: types, frontmatter parser, loader
internal/validate/   blocking errors and warnings
internal/index/      build pipeline, SQLite index, snapshot export
internal/scaffold/   the embedded project template
internal/project/    lorekeep-version, recent projects
internal/versions/   release cache, downloads, update check
internal/resolve/    query resolver (not built yet)
internal/mcp/        MCP server (not built yet)
internal/editor/     web editor, templ + htmx (not built yet)
testdata/world-ok/   the fixture world, shared by every consumer
```
