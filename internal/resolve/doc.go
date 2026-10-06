// Package resolve is the query resolver: the one place every read of the lore
// graph goes through, for the MCP server, the editor, and the graph views.
//
// A read is scoped along three axes, carried by a Context:
//
//   - worldline: which outcome each decision took. An entity, edge, statement
//     or belief whose valid_in does not hold under it does not exist in that
//     reading of the story.
//   - knower: whose mind the read is from. A scoped read shows a statement as
//     the knower believes it — their own belief, else their faction's, else
//     common knowledge — and leaves out what they are ignorant of.
//   - visibility: what a reader may see yet. Only internal is supported until
//     the spoiler filter is built; anything narrower fails rather than read at
//     internal, because a filter that silently does nothing leaks spoilers.
//
// The safety default is the awkward path. A zero Context is refused with
// ErrNoContext, so forgetting the scope fails at the first read instead of
// quietly reading everything. Omniscience — canon truth, no knower — exists
// only as Omniscient, a name a reviewer can grep for.
//
// Every read filters in the same order: branch, then status (canon only
// unless asked), then the knower's beliefs. Inverse and symmetric edges are
// derived here, at read time, from the authored edge; the index never stores
// them. Nothing in this package names a relation: inheritance follows the
// membership role, and agents are the types in the agent group.
package resolve
