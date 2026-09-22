# Implementation status

How much of [spec.md](spec.md) is built, as of **v0.2.0** (2026-09-23). The spec says
what the system should be; this file says what exists. Update it in the same commit
that changes the answer.

Legend: **done** — built and tested · **partial** — some of it, see notes ·
**not started**.

## Build order

The steps from the spec's *Implementation plan*.

| Step | Status | Where | Notes |
| --- | --- | --- | --- |
| 1 — lore-core v0.1: schema and build | done | lore-core v0.1.0 | Acceptance met: `lore build` emits index and snapshot, exits non-zero on dangling id, unknown relation, domain violation |
| 2 — lore-project-template | done | [lore-project-template](https://github.com/gmreyer/lore-project-template) | Pins v0.2.0; 10 files under `world/` incl. `.gitkeep`; CI on windows-latest green |
| 3 — Author twenty entities by hand | not started | a world repo | Needs a real project; no world repo exists yet |
| 4 — The resolver | not started | `internal/resolve/` | |
| 5 — MCP server | not started | `internal/mcp/` | |
| 6 — Editor | not started | `internal/editor/` | |
| 7 — Beliefs and branching | partial | | File format only: beliefs, statements, `valid_in`, decisions are parsed and checked for referential integrity. No inheritance, no worldline resolution |
| 8 — Graph projections | not started | | Roles exist in the schema for views to bind to |
| 9 — Godot runtime | not started | | The JSON snapshot it will load exists |

## By spec section

| Spec section | Status | Notes |
| --- | --- | --- |
| Entity and relation model | done | Opaque ids, charset `[a-z0-9_]`, aliases; core pack of 7 entity types, 8 roles, 14 relations |
| Schema packs: core plus project | partial | Core + project merge, `core_version` pin and drift check. Not built: the rename map for core upgrades |
| Statements, beliefs, and visibility | partial | Fields parsed and checked; `public` / `spoiler:<act>` / `internal` validated. Filtering belongs to the resolver |
| Branching canon | partial | `valid_in` and decision outcomes checked. Not built: branch-conditional percentage, blast-radius flag |
| Time model | partial | Eras, interval dates, ordering relations, lifespan warning. Not built: flagging a date that disagrees with an ordering edge |
| Repo layout and file format | done | `schema/` and `world/` required; missing either exits 2 |
| Build pipeline — outputs | partial | SQLite index with FTS5, JSON snapshot, findings on stderr. Not built: health metrics in the report |
| Build pipeline — blocking errors | done | Every item on the spec's list, plus `symmetric_mirror` and `duplicate_edge` |
| Build pipeline — warnings | partial | Lifespan, orphan, canon-on-draft, unbelieved statement, directory mismatch. Not built: conflicting inherited faction beliefs (needs inheritance) |
| Health metrics | not started | |
| Advisory lane | not started | Arrives with the MCP `validate` tool |
| Query resolution semantics | not started | Step 4 |
| MCP tool surface | not started | Step 5 |
| Web editor, wiki read mode, graph views | not started | Steps 6 and 8 |
| Godot runtime | not started | Step 9 |

## Tooling and release

| Item | Status | Notes |
| --- | --- | --- |
| CLI | done | `lore build <world-dir> [-out <dir>]`, `lore version`; exit 0 ok, 1 blocking errors, 2 unusable invocation or repository |
| CI | done | `go vet` and `go test` on windows-latest and ubuntu-latest |
| Release | done | A `vX.Y.Z` tag builds `lore.exe` (windows/amd64, no cgo) and creates a draft release with `SHA256SUMS` |
| Dependencies | as planned | `yaml.v3`, `modernc.org/sqlite`, `go-cmp`. `go-git` and the MCP SDK arrive with Steps 5–6 |

## Releases

| Version | Date | Contents |
| --- | --- | --- |
| v0.2.0 | 2026-09-23 | Missing `world/` exits 2; symmetric and inverse edges count against orphans; `symmetric_mirror`, `duplicate_edge`; `lore version`; first `lore.exe` |
| v0.1.0 | 2026-09-22 | Step 1: schema packs, file format, validator, index, CLI |
