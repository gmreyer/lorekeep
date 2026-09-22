# Lore Graph — System Design Spec


## Scope and locked decisions

A lore system for a single-player, story-focused game of 5–10 hours, authored by a team of 2–10 writers, with AI agents contributing drafts and driving in-game NPC dialogue.

| Decision | Choice |
| --- | --- |
| Source of truth | Git repo of Markdown + YAML frontmatter |
| Graph storage | Derived index, rebuildable, never authoritative |
| AI write access | Proposals only; humans promote to canon |
| Runtime relationship | Save-local overlay on a baked snapshot |
| Emergent to canon promotion | Not built — dropped as unnecessary |
| Belief modelling | Per-character, including false beliefs |
| Belief holders | Named cast plus factions, with inheritance |
| Canon branching | Yes, at major story decisions |
| Prose vs fields | Frontmatter canonical for graph, prose for narrative |
| Writer interface | Thin web app committing on their behalf |
| Engine | Godot |
| Consistency checking | Wired into the draft flow |
| Time model | Eras and relative ordering; fuzzy date intervals optional |
| Reuse across projects | Core schema pack plus per-project extensions |
| Wiki | A read mode of the editor, not a separate app |
| MCP server | Local, against each writer's checkout; hosted later if the team grows |
| Language | Go, with the editor UI in templ and htmx |
| Dev and writing platform | Windows |

Expected scale is 200–800 entities, perhaps 2,000 at the outside. That is small enough that several standard solutions are over-engineering, and this spec calls those out where they arise.

Explicitly out of scope: vector search, graph databases, a runtime query engine, shared-world persistence, and promotion of emergent play into canon. Each is noted below with the trigger that would bring it back.

## Architecture: two layers

### L0 — Canon

A git repository of authored lore. Reviewed, human-controlled, slow-moving. At build time it compiles to an immutable snapshot that ships with the game. The runtime never writes here.

### L1 — Play record

A per-save, append-only event log layered over the L0 snapshot. Reads resolve as *snapshot base plus session deltas*. Nothing is mutated in place.

Append-only is the important part. It buys replay, rollback, deterministic debugging, and a clean answer to "why does this NPC believe that?" — you can walk the log. In-place mutation of a loaded graph would be simpler to write and considerably worse to live with, because narrative bugs surface hours into a playthrough and are otherwise unreproducible.

### What was deliberately not built

An earlier draft of this design had a third layer promoting emergent play events into canon. It is dropped. Without it, the whole review pipeline for generated content, cross-save deduplication, and the question of whose playthrough counts all disappear.

If promotion ever becomes interesting, an export tool that converts a session log into draft proposals sits on top of this design without changing it.

### Forward compatibility with a shared world

"Single-player now, shared world later" costs almost nothing to keep open, provided two fields go into every L1 event from day one:

- a **world ID**, which is constant in single-player
- a **causal ordering field** — a Lamport counter or similar — which is redundant when there is one writer

With those present, the same log format works unchanged when a server is the one appending. Build nothing else for that future.

## Entity and relation model

### Identity

Every entity has a **stable, opaque ID** that never changes: `char_kaelen_vor`, or a ULID if you prefer no semantics at all. It is decoupled from filename, title, and display name.

This is the one mistake in this design that is genuinely expensive to undo. Characters get renamed, files get reorganised, and every edge, belief, and save file points at these IDs. Renaming is free when IDs are stable and a migration when they are not.

Every entity also carries an `aliases` list. Characters in fiction have titles, epithets, false names, and names only certain factions use. Aliases drive search, editor autocomplete, and an agent's ability to resolve a name it read in prose.

### Entity types

A small closed set, extended only by deliberate schema change:

- `character` — an individual, living or dead
- `faction` — any organised group that can hold a position
- `location` — place, at whatever granularity
- `event` — something that happened at a point or interval in time
- `object` — artefact, item, significant thing
- `concept` — magic system, religion, law, technology, custom
- `decision` — a branch point in the story (see the branching section)

`character` and `faction` are both **agents**, meaning they can hold beliefs. That abstraction matters in the next section.

### Relation vocabulary

The value of the graph is in the constrained edge vocabulary, not the freedom to link anything to anything. Freeform labels guarantee that `killed`, `slew`, `murdered_by`, and `caused_death_of` all appear within a month, and nothing is queryable.

Rules:

- A **closed core set** of relation types, each with declared direction, domain, and range.
- Adding a relation type is a schema change, reviewed like any other.
- CI fails the build on unknown relation types.
- Inverses are declared once and derived, never authored twice.

A starting core set, expected to grow to perhaps 30–40 types:

| Relation | Domain → Range |
| --- | --- |
| `member_of` | character → faction |
| `allied_with` / `hostile_to` | faction → faction |
| `parent_of`, `sibling_of`, `married_to` | character → character |
| `located_in` | location → location, object → location |
| `originates_from` | character → location |
| `participated_in` | character → event, faction → event |
| `occurred_at` | event → location |
| `caused` | event → event |
| `owns` / `created` | agent → object |
| `practises` / `forbids` | agent → concept |
| `mentions` | any → any |

`mentions` is the safety valve: an untyped soft link for "these are related and I can't say how yet." Keep it, and keep an eye on its share of total edges. A corpus where `mentions` dominates is one where the vocabulary is missing something.

### Schema packs: core plus project

The system is meant to be reused across projects, so the vocabulary is not fixed by this spec. It is a **setup step**, decided with the first project and extended by it and by later ones.

Two tiers:

- **Core pack** — ships with the system, project-independent, structural. Identity, membership, containment, participation, causation, ownership. These are the relations any fictional world needs and that the tooling itself depends on.
- **Project pack** — defined per world at setup and extended as the world grows: `sworn_to`, `bound_by_oath`, `descended_from_line`, whatever that particular fiction requires.

Initialising a project scaffolds `schema/` from the core pack at a pinned version, plus an empty project pack. The first project fills its own in during authoring; later projects start from the same core and diverge from there.

**Promotion upstream.** A project relation that proves general gets promoted into the core pack in a new version. Projects pin a core version and upgrade deliberately, and each core release carries a rename map so migration is mechanical rather than a judgement call per entity.

**The requirement this places on everything else: all tooling must be schema-driven, never hardcoded.** Editor forms, validation rules, MCP tool descriptions, and graph projections are generated from the schema files. Anywhere a relation name appears literally in code, the system stops being universal.

For the graph projections in particular, bind views to **roles** rather than relation names. The geography view draws whatever relation is tagged `role: containment`; the faction tree draws whatever is tagged `role: membership`. A project that names containment something else still gets both views for free.

What does not change: the vocabulary stays **closed within a project**. Universal means different per project, not freeform.

## Statements, beliefs, and visibility

This is the most expensive part of the system and the most valuable for a story-driven game. The design below is shaped mainly by keeping it affordable.

### Reify selectively

The trap is attaching belief metadata to every edge. Instead, contested facts get promoted to **Statement** nodes:

- A Statement makes a subject–relation–object triple explicit and carries its own canon truth value: `true`, `false`, or `unresolved`.
- `(Agent) -[believes {confidence, since, acquired_from}]-> (Statement)`
- A **false belief** is simply an agent believing a Statement whose canon value is `false`. No special machinery.
- **Absence of an edge is ignorance.** Never model ignorance explicitly; the graph would triple in size for no gain.

The discipline that makes this affordable: **plain edges by default, Statements only when someone needs to be wrong.** Most of the graph is uncontested — `born_in`, `member_of` — and stays as simple typed edges. Promoting an edge to a Statement is an explicit authoring act, performed when a character's misapprehension matters to a scene.

### Lying comes free

`(Agent) -[asserts]-> (Statement)` while the agent believes its negation is, structurally, a lie. For a story game this is probably the feature rather than a side effect: you can query for every character currently deceiving another, trace how a rumour propagated through `acquired_from` chains, and let a narration agent know it is speaking to someone who is lying.

### Faction inheritance

Make **Agent** the thing that holds beliefs, with both characters and factions being agents. Belief resolution is then a lookup chain:

1. The character's explicit belief about the Statement
2. Inherited from their factions, in declared priority order
3. Canon truth, if the Statement's visibility marks it common knowledge
4. Otherwise, ignorance

Author the faction's position once; override only where a character is unusual — which is precisely where the interesting characters are. It also makes divergence *queryable*: a character who contradicts their faction's belief is structurally a heretic or a defector.

**Edge case to handle:** dual membership with conflicting faction beliefs. Declared priority order on the character's `member_of` edges resolves it deterministically, and CI flags unresolved conflicts for an author to decide.

### Three independent axes — do not collapse them

| Axis | Question | Consumer |
| --- | --- | --- |
| Canon truth | Is it true in this worldline? | Writers, validation |
| Character knowledge | Does this agent know or believe it? | NPC dialogue, narration |
| Player visibility | May the reader see it yet? | Wiki, spoiler gating |

These are genuinely independent. A fact can be true, known to the speaker, and still a spoiler the wiki must hide from a reader on their first playthrough. Player visibility is **not** derivable from the belief graph and needs its own field: `public`, `spoiler:<act>`, or `internal`.

## Branching canon

### Do not use git branches for story branches

This is the strongest warning in the spec. Git branches assume eventual merge; story branches diverge permanently and must all be queryable at once, side by side, forever. Using git branches here means fighting the tool indefinitely and losing the ability to ask "what is true in ending B?" without a checkout.

Model branching as **data inside a single canon**.

### Decisions, conditions, worldlines

- **Decision nodes** are first-class entities: `decision:siege_of_vale`, with a declared set of outcomes.
- Any node, edge, or Statement may carry an optional `valid_in` condition naming decision outcomes. **The default is unconditional** — valid in every branch.
- A **worldline** is an assignment of outcomes to decisions. Every read resolves against one.

### Why this unifies well

- The player's save holds their outcome selections, so branch resolution is just part of the L1 overlay that already exists. No separate mechanism.
- A Statement's canon truth value can be branch-dependent, so a character can be right in one worldline and wrong in another **without duplicating the character**.
- The wiki gets a worldline selector at the top and otherwise works unchanged.
- Writers can ask the system what differs between two endings, which is a real authoring tool rather than a side effect.

### The failure mode, and the guard

Authors tag everything as conditional until the combinatorics are unreadable and no one can say what is true unconditionally.

Guard it in CI:

- Report the percentage of facts that are branch-conditional. In a well-structured 5–10 hour story, expect **under 10%**.
- Flag any decision whose blast radius exceeds a threshold — that usually means the decision is doing too much narrative work and should be split, or that lore is being conditioned when it needn't be.
- Flag conditions referencing outcomes that no longer exist.

## Time model

**Decided.** Approved as recommended below; this section is now locked.

**Recommendation: eras plus relative ordering, with optional fuzzy dates.**

Reasoning: precise calendar dates are a commitment you cannot easily walk back, and most of what a lore system needs to answer is ordering — did this happen before that, was this character alive then, could these two have met. Precise dates buy little and cost a lot of authoring friction, because every new event forces a decision about exactly when.

The model:

- **Eras** are named, ordered intervals — the coarse spine of the timeline.
- Events carry an optional date as an **interval with confidence**, not a point: `{era: "third_reign", earliest: 412, latest: 418, precision: "approximate"}`. All fields optional.
- Ordering edges are first-class and always authoritative: `before`, `after`, `during`, `concurrent_with`. Where a date and an ordering edge disagree, **the edge wins** and CI flags the conflict.
- If you later want a full calendar, define it as a formatting layer over an integer day count. Do not retrofit one into the authoring format.

**Keep in-world time strictly separate from editorial history.** In-world dates are content; who changed what and when is git's job. Conflating them is a common and irritating mistake — you end up unable to ask either question cleanly.

One validation rule worth having early: flag any character participating in an event outside their lifespan interval. It catches a surprising share of real continuity errors at near-zero cost.

## Repo layout and file format

### Why git is the source of truth

For 2–10 writers the hard problem is not querying, it is **review and history**. Git provides diffs, blame, branches, and PR review at no cost, and those are exactly the primitives that make AI contribution safe. A graph database as primary would mean hand-building versioning, review queues, and rollback — weeks of work reproducing something git already does well.

Two costs to accept: no transactional integrity across files, and writers need a wrapper (see the editor section). Flip to a Postgres-primary design only if the runtime needs live writes or the corpus exceeds roughly 100,000 entities. Neither applies here.

### Layout

```
world/
  characters/kaelen-vor.md
  factions/the-ashen-court.md
  locations/vale-of-orrin.md
  events/siege-of-vale.md
  objects/
  concepts/
  decisions/siege-outcome.md
  statements/            # contested facts
schema/
  entity-types.yaml
  relations.yaml         # project pack, closed vocabulary, with inverses
  eras.yaml
go.mod                   # pins the lore-core version
.gitattributes           # eol=lf, see the Windows notes
build/                   # derived, gitignored
```

`schema/` and `world/` must both exist; a build without either exits as an unreadable repository, not as a world with errors. Git stores no empty directories, so a world with no content yet commits `world/.gitkeep`.

### Entity file

```yaml
---
id: char_kaelen_vor
type: character
name: Kaelen Vor
aliases: ["The Ashen Knight", "Vor"]
status: canon          # draft | canon | deprecated | non_canon
visibility: public     # public | spoiler:act2 | internal
lifespan: { era: third_reign, birth: 380, death: 419 }
relations:
  - { type: member_of, target: fac_ashen_court, priority: 1 }
  - { type: originates_from, target: loc_vale_of_orrin }
  - { type: participated_in, target: evt_siege_of_vale }
beliefs:
  - { statement: stmt_orrin_betrayal, value: true, confidence: high,
      acquired_from: char_miren }
valid_in: null         # unconditional
---

Kaelen Vor came to the Vale as a conscript...
```

### The rule that keeps the graph honest

**Frontmatter is canonical for the graph; prose is canonical for narrative.** Relations, IDs, aliases, dates, beliefs, and statuses live in fields, and nothing is ever parsed out of prose.

CI blocks on referential integrity only — dangling IDs, unknown relation types, invalid enum values. Semantic agreement between prose and fields is an **advisory** AI linter that flags drift for humans, never a blocking check. A hard gate there would be wrong often enough to train the team to bypass it, which is worse than having no check at all.

## Build pipeline and validation

One command compiles the repo into a derived index. It is disposable and rebuildable, which is what lets you change graph technology later without migrating the source of truth.

### Outputs

1. **SQLite index** — entities, edges, statements, beliefs, plus FTS5 over names, aliases, and prose. Built with `modernc.org/sqlite`, so no cgo and no C toolchain. Used by the editor, the wiki, and the MCP server.
2. **Game snapshot** — a single JSON file shipped with the build (see the Godot section).
3. **Validation report** — errors, warnings, and health metrics.

At this scale the build is a few seconds. Run it on every commit and on every save in the editor.

**Not included: vector search.** With a few hundred entities, exact matching over names and aliases outperforms semantic similarity and is easier to debug. Revisit above roughly 5,000 entities.

### Blocking errors

- Dangling reference: an ID that does not exist
- Unknown relation type, entity type, or era
- Relation violating declared domain or range
- `valid_in` naming a decision outcome that does not exist
- Belief pointing at a non-existent Statement
- Duplicate ID
- Two IDs or filenames colliding case-insensitively (see the Windows notes)

### Warnings

- Character participating in an event outside their lifespan
- Statement with no believers, or believed by no one and asserted by no one
- Conflicting inherited faction beliefs with no explicit override and no priority order
- Orphan entity: no inbound edges from any canon entity
- Canon entity depending on a `draft` entity

### Health metrics

Tracked over time, because the trend matters more than any single value:

- Percentage of facts that are branch-conditional (target: under 10%)
- Blast radius per decision
- Share of edges that are `mentions` (rising means the vocabulary is missing a type)
- Statement count versus plain edge count (rising fast means reification discipline is slipping)
- Entities with no prose body

### The advisory lane

Separate from CI, an AI consistency pass runs on proposals and reports contradictions in natural language. It never blocks a merge. This is the `validate` tool from the MCP section, wired into the draft flow — see *On consistency checking* for the reasoning.

## Query resolution semantics

Every read — wiki, editor, MCP tool, in-game narration — goes through one resolver with the same three parameters. Implement this once and share it everywhere; divergent resolution logic between the wiki and the runtime is how spoilers leak.

### The query context

| Parameter | Meaning | Default |
| --- | --- | --- |
| `worldline` | Decision outcome assignments | All-unconditional baseline |
| `knower` | Whose knowledge to filter through | **None — must be explicit** |
| `visibility` | Max spoiler tier to reveal | `public` |

### Resolution order

1. **Branch filter.** Drop anything whose `valid_in` conflicts with the worldline.
2. **Belief resolution**, if a `knower` is set. Walk the chain: explicit belief → faction inheritance by priority → canon truth if common knowledge → ignorance. Return what the knower believes, not what is true.
3. **Visibility filter.** Drop anything above the requested spoiler tier.
4. **Status filter.** Exclude `draft`, `deprecated`, and `non_canon` unless explicitly requested.

### The safety default that matters most

A narration agent must query **through the belief layer, never against raw canon**, or it will have characters reference things they cannot possibly know. That is the single most likely bug in the whole system, and it produces subtle lore leaks rather than crashes, so it will not be caught by tests.

Therefore: **unscoped canon access requires an explicit flag.** Make the omniscient read the awkward one. Writers and validation tools pass the flag knowingly; dialogue code cannot reach omniscience by accident.

### Two useful derived queries

- `diff(worldline_a, worldline_b)` — what differs between endings. A genuine authoring tool.
- `divergence(agent)` — where this agent's beliefs depart from their faction's, or from canon truth. Finds the heretics, the deceived, and the liars.

## MCP tool surface

"MCP friendly" is a property of the tool surface, not the data model. The failure mode is exposing a `run_query` tool and calling it done: agents write bad queries and dump enormous results into context.

### The scale changes the design

With 200–800 entities, an agent can hold a summary of the entire world in context. This is a significant advantage over larger corpora and should be exploited:

**`get_world_index`** returns every entity as ID, name, type, and a one-line summary — a few thousand tokens for the whole world. An agent that has this stops guessing what to search for, which is the largest single source of retrieval failure. At this scale it is more reliable than any retrieval scheme.

### Tools

| Tool | Purpose |
| --- | --- |
| `get_world_index` | The whole entity index, compact |
| `search` | Name, alias, and full-text; returns IDs with snippets |
| `get_entity` | Full record, resolved through the query context |
| `expand` | N-hop neighbourhood, filtered by relation type, token-budgeted |
| `get_beliefs` | What this agent believes, with provenance |
| `diff_worldlines` | What differs between two branches |
| `validate` | Run the consistency pass over a proposed change |
| `propose_change` | Write a draft proposal — never canon |

Every tool takes the query context from the previous section. Every result cites stable entity IDs so an agent's output can be traced back and verified.

### Write path

`propose_change` creates a draft commit or PR; a human promotes it. The reasoning is compounding error: invented lore gets read by the next agent, cited as fact, and amplified. Reviewing a diff is cheap; excavating fabricated canon six months later is not.

### On consistency checking

Consistency checking was not selected as a desired agent capability, and this spec includes it anyway — as the `validate` tool wired into the draft flow rather than as a standalone feature.

The argument: agents are drafting lore, which means generated content enters a corpus that other agents later read as fact. Validation is the counterweight. At this scale it is nearly free, because a few hundred entities means the validating agent can hold the relevant neighbourhood in context and actually reason about contradictions rather than sampling a subset and hoping.

**Decided: wired into the draft flow.** `validate` runs on every proposal before a human sees it, and its findings attach to the proposal as review notes. It reports contradictions but does not block — a human still promotes. Treat a proposal that fails validation as a signal about the drafting prompt, not only about that one draft.

This makes `validate` a Phase 2 deliverable alongside the rest of the MCP surface, since the draft flow depends on it from the first agent-authored entity.

### Deployment: local, with a hosted path left open

The MCP server runs **locally, against the writer's own checkout** and the index built from it. No infrastructure, no auth, no sync, and the agent sees exactly the branch that writer is on — including uncommitted drafts, which is what makes drafting against work-in-progress possible at all.

One consequence worth naming: each writer's agent sees a different world. A proposal drafted against a stale checkout can conflict with canon that moved underneath it. The fix is cheap — run `validate` against the merge target in CI as well as locally, so the agent's local pass is a fast first opinion and the authoritative check happens where the merge happens.

**Keeping the hosted upgrade cheap.** The server's only inputs should be the schema packs and a path to the built index. No dependence on local filesystem layout beyond one configured root, and no assumption that a git working tree is present. Hold to that and hosting later is a configuration change rather than a rewrite.

Triggers for making the switch: writers stepping on each other's drafts, or wanting agent access for people without a checkout — the same non-git readers that read mode serves. Note that in-game narration is **not** a trigger: the runtime talks to a shipped snapshot, never to this server.

### Runtime narration

The in-game agent uses the same resolver with `knower` always set to the speaking character. It should not have access to `propose_change` or to unscoped canon at all. Give it a restricted tool set rather than trusting it to filter its own reads.

## Web editor

Everything else in this spec is schema and a build script. **The editor is the real software cost**, and the only component that needs sustained engineering attention.

### Keep it thin

- A Markdown body field
- A form over frontmatter, generated from the schema files
- Commit on save, with a message; branch and PR for canon promotion
- Git access via `go-git`, which is pure Go — writers never see a terminal and need no git installation
- Served by the same Go binary as the CLI, rendered with templ and htmx

### The one feature that determines data quality

**A relation picker with autocomplete over existing entity IDs and aliases.**

If adding a relation is harder than writing a sentence, writers will put the fact in prose and the graph will quietly rot. Every relation that exists only in prose is invisible to agents, to validation, and to the runtime. This single interaction is worth more to the health of the corpus than any validation rule.

Corollary: creating a new entity from inside the picker — type the name, get an ID, fill it in later — must be one click. Otherwise writers avoid linking to things that do not exist yet, and the graph only ever records the past.

### Also worth building early

- **Worldline selector**, so writers read their world as a player in a given branch would
- **Validation panel** showing errors and warnings for the current entity inline, not in a separate CI report nobody reads
- **Belief editor** presented as "what does this character think is true?" rather than as graph editing. The underlying Statement model should be almost invisible to writers

Spoiler-tier preview is deliberately absent from this list: it falls out of read mode, below, rather than being built separately.

### The wiki is a read mode of the editor

Not a separate application. Both go through the same resolver, so a second implementation would only be a second place for spoiler filtering to be wrong.

What differs is not the code but the **session role**, which fixes the query context:

|  | Read mode | Edit mode |
| --- | --- | --- |
| Visibility | `public` by default, selectable up to the reader's clearance | `internal` |
| Status filter | Canon only | All, draft included |
| Worldline | Reader picks | Author picks |
| Git access | None required | Commit rights |

Two things this buys immediately. Readers without commit rights get accounts — QA, contractors, voice actors, and a public wiki later — without a separate deployment. And the spoiler-tier preview listed above is no longer a feature to build: it is read mode, viewed from inside the editor.

**The one real risk** is that a shared codebase makes it easy for internal material to leak into read mode through some path that forgot to pass the context. Guard it structurally: the query context must be derived from the session role on the server, never from UI state or a client-supplied parameter. That is a small rule that is very hard to violate accidentally, and the alternative is a class of bug you will only find by having a reader discover it.

A fully public wiki, if it ever happens, is read mode with a fixed anonymous role — optionally rendered to static pages from the built index so nothing user-facing touches the live editor at all.

### Graph views: projections, not one explorer

One narrow thing is not worth building: a single force-directed view of the entire world, unfiltered. It demos beautifully and is used approximately twice. What earns its place instead is a set of constrained projections, each with a layout determined by the relation type it draws. Fixing the layout is what makes them readable, and it also makes each one individually simpler to build than a general explorer would be.

- **Event sequence** — events laid along the era spine, with `caused` edges drawn as links. Filterable by participant, faction, or location.
- **Faction membership** — a tree from `member_of`, with belief divergence highlighted so heretics and defectors are visible at a glance.
- **Geography** — `located_in` containment, or a real map if you have one, with characters, events, and objects placed on it.
- **Ego network** — one entity out to N hops, filtered by relation type. The "how does this character connect to anything" view.

All four go through the same resolver as every other read, which means they inherit worldline, knower, and visibility for free. "What did the faction tree look like in ending B, as far as this character knew" is then a parameter change rather than a feature.

Build cost: these follow the editor rather than shipping with it, since each depends on the relation vocabulary having settled. The event sequence view is likely the highest value first, because a 5–10 hour story is mostly a chronology and continuity errors cluster there.

## Godot runtime

### No database at runtime

At a few hundred entities the entire graph is single-digit megabytes. Ship the snapshot as one JSON resource, load it into memory at startup, traverse in GDScript or C#. No SQLite, no query engine, no indexes to tune. SQLite stays on the authoring side only.

This is a real simplification, not a shortcut — it removes a dependency, a plugin, and an entire class of platform-specific build problems.

### Save structure

The save holds three things:

1. **Worldline** — the player's decision outcomes so far
2. **Event log** — append-only, with world ID and causal counter per entry
3. **Derived belief deltas** — what characters have learned or been told during play

On load: deserialise the snapshot, replay the log over it, get the current world state. At this scale replay is fast enough to do unconditionally, which is worth more than the milliseconds a snapshot cache would save — it means the log is always the truth and can never drift from a cached state.

### Dialogue integration

The narration agent gets a restricted client with `knower` bound to the speaking character and no way to override it. Two rules worth enforcing in code rather than in a prompt:

- A character cannot reference a Statement they have no `believes` edge to.
- Learning during dialogue appends a belief event to the log with `acquired_from` set to the speaker — which is what makes rumour propagation traceable afterwards.

### Latency

LLM narration has unpredictable latency and will occasionally fail outright. Design the dialogue system so that every generated line has an authored fallback, and so the graph query itself — which is fast and deterministic — is separable from the generation step. Never let a failed API call block story progression.

## Resolved questions and build order

### Questions raised, and where they landed

1. **Relation vocabulary** — the core set here is a starting point. It needs one pass with actual lore in hand before it is locked.
2. **Wiki as separate app or a read mode of the editor** — probably the latter, given the same resolver serves both.
3. **Where the MCP server runs** — locally against a repo checkout, or hosted against the built index.

All three are now resolved. The vocabulary is a per-project setup step rather than something this spec fixes — see *Schema packs*. The wiki is a read mode of the editor. The MCP server runs locally against each writer's checkout, with the hosted path kept open — see *Deployment*.

Nothing structural is outstanding. The next real decisions come from authoring: the first twenty entities will tell you whether the relation vocabulary and the reification discipline survive contact with actual lore.

### Suggested build order

**Phase 1 — Schema and validation.** Core schema pack, project pack scaffolding, file format, build script, blocking validations. Hand-author 20 entities to shake out the relation vocabulary before anything is built on top of it. Do not skip this; every later component depends on the vocabulary being roughly right.

**Phase 2 — Resolver and MCP.** The query resolver with all three context parameters, then the MCP tools over it. Available to agents before any UI exists, which is fine and useful — writers can talk to the world through a chat client while the editor is still being built.

**Phase 3 — Editor.** Body field, frontmatter form, relation picker, validation panel. The largest single chunk of work.

**Phase 4 — Branching and beliefs.** Decisions, `valid_in`, Statements, belief inheritance, and the editor affordances for all of it. Deliberately later: these are the parts most likely to change shape once writers have worked in the system for a while, and building them early means rebuilding them.

**Phase 5 — Runtime.** Snapshot export, Godot loader, save overlay, restricted narration client.

### The order's one strong opinion

Beliefs and branching are the most interesting parts of this design and the most tempting to build first. Building them before writers have authored real lore in the plain graph is likely to produce an elegant model of the wrong thing. Let the vocabulary and the authoring loop settle, then add the layers that depend on them.

## Implementation plan

### Repo topology

A single template repo is not enough, because the schema packs promote upstream: a relation that proves general moves into the core pack in a new version, and projects pin a version and upgrade. A copied template can never receive those updates, so every project would fork the tooling on day one and drift permanently.

Split in two:

- **`lore-core`** — tooling and the core schema pack. A Go module, versioned with semver git tags and required by each project. Never copied into a project. For a private repo, every developer sets `GOPRIVATE=github.com/<org>/*` once; put that in the README, because it is the one thing that will confuse someone on day one.
- **`lore-project-template`** — the git template: directory skeleton, a `go.mod` requiring a pinned `lore-core` version, an empty project pack, the CI workflow, and a small fixture world. Cloned per project, and almost entirely empty.

The test for where something belongs: **would a fix to it need to reach existing projects?** If yes, it lives in `lore-core`.

### Stack

**Go throughout, with one exception.** The CLI, validator, index builder, resolver, MCP server, and editor backend are all Go. Only the editor's browser UI needs anything else.

Go is the right choice here for reasons beyond the team's familiarity with it:

- **A single static binary.** Writers on Windows get `lore.exe` — no runtime to install, no `node_modules`, no PATH surgery. Given that the audience includes non-technical writers, this is close to decisive on its own.
- **The official MCP Go SDK** is past v1 and maintained in collaboration with Google, so the Step 5 server is not a bet on a young library.
- **`modernc.org/sqlite` is pure Go** with FTS5 enabled and no cgo, so no C toolchain on any machine and clean cross-compilation. The cgo-based driver would require MinGW on every Windows dev box.
- **Go modules pin by git tag natively.** That is exactly the core-pack versioning model described above, with no registry and no publish step.
- **`go-git` is pure Go**, so the editor commits on a writer's behalf without git installed locally.

Dependencies stay few: `gopkg.in/yaml.v3` for frontmatter, `modernc.org/sqlite`, `go-git`, the MCP SDK, and `go-cmp` in tests.

**Testing** is `go test` with `testdata/` golden files and an `-update` flag to regenerate them. Regeneration being explicit rather than a keystroke matters for the resolver goldens, where a diff that looks cosmetic may be a visibility filter breaking.

**The browser UI is the exception.** Start with templ and htmx, server-rendered from the Go binary — the relation picker is close to htmx's canonical example and needs perhaps thirty lines of JavaScript. Defer a real frontend bundle until Step 8, where the graph projections want a visualisation library, and add it as one island rather than rewriting the editor.

### Windows as the primary platform

Developers and writers are all on Windows, which constrains the pipeline more than it constrains the code.

- **Line endings.** Commit `.gitattributes` with `* text=auto eol=lf` before the first content file lands. The `world/*.md` files are the source of truth, and CRLF churn would put every line of every file into every diff, destroying the review workflow this whole design rests on.
- **Case-insensitive filesystem.** Windows treats `Kaelen-Vor.md` and `kaelen-vor.md` as one file; git does not. Two IDs differing only in case work for one writer and collide for another, so the validator errors on case-insensitive collisions.
- **No Make.** Use Task (`Taskfile.yml`, itself a single Go binary) or plain `go run ./cmd/lore`. A Makefile would mean a Unix toolchain on every machine.
- **Keep clones out of OneDrive.** Windows defaults `Documents` to OneDrive sync, and a synced git repo produces file locks and phantom conflicts that read as tooling bugs. Say so in the README — writers clone wherever Explorer opens.
- **Long paths.** `git config --global core.longpaths true`. The layout is flat, so this is precautionary.
- **File watching** for editor live-rebuild uses `fsnotify`, noisier on Windows; debounce rebuilds by a couple of hundred milliseconds.

### Step 1 — `lore-core` v0.1: schema and build

The schema pack format comes first, since everything else is generated from it: relation definitions with direction, domain, range, inverse and a `role` tag; entity types; eras. Then the validator and index builder over them. Ship the core pack with only structural relations and resist anything world-flavoured.

*Done when:* `lore build` on a fixture world emits the SQLite index and the JSON snapshot, and exits non-zero on a dangling ID, an unknown relation type, and a domain violation.

### Step 2 — `lore-project-template`

Skeleton, version-pinning config, empty project pack, CI workflow running `lore build`, and a five-entity fixture world so a fresh clone proves itself.

*Done when:* clone, `go build ./...`, CI green — and deliberately breaking one reference turns it red.

### Step 3 — Author twenty entities by hand

Before any further tooling. This is the vocabulary shakeout, the step most likely to be skipped and most expensive to skip. Extend the project pack freely while writing; that friction is the signal.

*Done when:* several days pass without needing a new relation type, and `mentions` is a small share of edges. If `mentions` dominates, the vocabulary is still missing something — keep authoring rather than building on top of it.

### Step 4 — The resolver

Worldline, knower, visibility, in `lore-core`. Everything downstream reads through it, so it lands before anything that would otherwise implement its own filtering.

*Done when:* golden tests show one entity rendering three ways under three contexts, and an omniscient read requires the explicit flag.

### Step 5 — MCP server

`get_world_index`, `search`, `get_entity`, `expand`, `validate`, `propose_change`, over the resolver. Local, taking only the schema packs and an index path as inputs.

*Done when:* an agent drafts an entity into a branch, `validate` catches a deliberately planted contradiction, and nothing the agent can call touches canon directly.

This is the first point at which the system is genuinely useful: authoring through a chat client, before any UI exists.

### Step 6 — Editor

Body field, schema-generated frontmatter form, relation picker with autocomplete, validation panel, and read mode driven by session role. The largest chunk of work by a wide margin.

*Done when:* a writer who has never seen the repo adds a linked entity without help, and read mode cannot surface an `internal` entity even given a hand-crafted request.

### Step 7 — Beliefs and branching

Statements, `valid_in`, decision nodes, faction inheritance, and the editor affordances that keep the Statement model invisible to writers.

*Done when:* `diff_worldlines` returns something a writer finds useful, and `divergence` finds a character known to disagree with their faction.

### Step 8 — Graph projections

Event sequence first. Bound to role tags rather than relation names, or the second project inherits none of them.

This is where JavaScript enters the editor for the first time, since the projections want a real visualisation library. Add it as one island beside the server-rendered forms rather than rewriting them.

### Step 9 — Godot runtime

Snapshot loader, save overlay with replay, restricted narration client with `knower` bound and no override.

*Done when:* a character provably cannot reference a Statement they have no belief edge to, and a failed generation call falls back to authored lines without blocking progression.

### On handing this over

The acceptance criteria matter more than the prose: they are what makes "done" checkable by someone who did not sit through the design. Hand over one step at a time rather than the whole sequence — steps 3 and 6 will change your mind about details in the steps that follow them.
