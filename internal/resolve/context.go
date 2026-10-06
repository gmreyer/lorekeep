package resolve

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/gmreyer/lorekeep/internal/world"
)

var (
	// ErrNoContext is a read with a zero Context. Build one with Scoped or
	// Omniscient.
	ErrNoContext = errors.New("no query context: use resolve.Scoped or resolve.Omniscient")
	// ErrNoKnower is a scoped context with an empty knower.
	ErrNoKnower = errors.New("a scoped context needs a knower")
	// ErrVisibilityUnsupported is any visibility narrower than internal, until
	// the visibility filter is built.
	ErrVisibilityUnsupported = errors.New("only internal visibility is supported")
	// ErrUnknownEntity is an ID that names nothing present under the context.
	ErrUnknownEntity = errors.New("unknown entity")
	// ErrNotAgent is a knower, or a mind being read, whose type is not in the
	// agent group.
	ErrNotAgent = errors.New("not an agent")
	// ErrUnknownOutcome is a worldline naming a decision or an outcome the
	// world does not declare.
	ErrUnknownOutcome = errors.New("unknown decision outcome")
	// ErrOutOfScope is a read the context does not permit: one agent reading
	// another's mind, or an authoring read from a scoped context.
	ErrOutOfScope = errors.New("out of scope for this context")
	// ErrUnknownRelation is a relation name the vocabulary does not declare.
	ErrUnknownRelation = errors.New("unknown relation")
	// ErrUnknownStatus is an empty or unknown status set from WithStatuses.
	ErrUnknownStatus = errors.New("unknown status")
)

// Worldline assigns outcomes to decisions: decision ID → outcome. A decision
// it leaves out is unassigned, and a condition on it does not hold, so the
// empty worldline reads only what is true in every branch.
type Worldline map[string]string

// Context is the scope of one read. Its zero value is deliberately invalid;
// build one with Scoped or Omniscient.
type Context struct {
	set        bool
	omniscient bool
	knower     string
	worldline  Worldline
	opts       options
}

type options struct {
	statuses   map[string]bool
	visibility world.VisibilityKind
}

// Option adjusts a Context.
type Option func(*options)

// WithStatuses sets which statuses are read. The default is canon only;
// drafts, deprecated and non-canon appear only when named here.
func WithStatuses(s ...world.Status) Option {
	return func(o *options) {
		o.statuses = make(map[string]bool, len(s))
		for _, st := range s {
			o.statuses[string(st)] = true
		}
	}
}

// WithVisibility sets the reader's visibility. Only internal is accepted until
// the filter is built; anything else makes every read fail with
// ErrVisibilityUnsupported.
func WithVisibility(v world.VisibilityKind) Option {
	return func(o *options) { o.visibility = v }
}

// Scoped reads the world as knower sees it under wl.
func Scoped(wl Worldline, knower string, opts ...Option) Context {
	return Context{set: true, knower: knower, worldline: maps.Clone(wl), opts: build(opts)}
}

// Omniscient reads canon truth under wl, with no knower. It is the authoring
// read, and the only way to get one.
func Omniscient(wl Worldline, opts ...Option) Context {
	return Context{set: true, omniscient: true, worldline: maps.Clone(wl), opts: build(opts)}
}

func build(opts []Option) options {
	o := options{
		statuses:   map[string]bool{string(world.StatusCanon): true},
		visibility: world.VisibilityInternal,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Worldline returns a copy of the context's worldline.
func (c Context) Worldline() Worldline { return maps.Clone(c.worldline) }

// Knower returns the knower of a scoped context.
func (c Context) Knower() (string, bool) { return c.knower, !c.omniscient && c.set }

// IsOmniscient reports whether the context reads canon truth.
func (c Context) IsOmniscient() bool { return c.omniscient }

// admits reports whether a status is read under the context.
func (c Context) admits(status string) bool { return c.opts.statuses[status] }

// valid checks what the context can check without the world.
func (c Context) valid() error {
	switch {
	case !c.set:
		return ErrNoContext
	case !c.omniscient && c.knower == "":
		return ErrNoKnower
	case c.opts.visibility != world.VisibilityInternal:
		return ErrVisibilityUnsupported
	case len(c.opts.statuses) == 0:
		return fmt.Errorf("%w: WithStatuses needs at least one status", ErrUnknownStatus)
	}
	for _, s := range slices.Sorted(maps.Keys(c.opts.statuses)) {
		if !world.Status(s).Valid() {
			return fmt.Errorf("%w: %q", ErrUnknownStatus, s)
		}
	}
	return nil
}
