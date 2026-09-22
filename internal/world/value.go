package world

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Enum values parse leniently and validate strictly. A bad status decodes into
// the field verbatim and is reported by internal/validate, with a file and a
// line, alongside every other fault in the world. Failing inside the YAML
// decoder instead would abandon the rest of the file and locate the fault only
// as well as yaml.v3 happens to.

// Status is an entity's or statement's standing in canon.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusCanon      Status = "canon"
	StatusDeprecated Status = "deprecated"
	StatusNonCanon   Status = "non_canon"
)

// Valid reports whether the status is one of the declared four. Empty is not
// valid: status is required, and a default would silently promote a writer's
// omission into a claim about canon.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusCanon, StatusDeprecated, StatusNonCanon:
		return true
	}
	return false
}

// Confidence is how firmly an agent holds a belief. Empty means unstated,
// which is the common case and carries no claim.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

func (c Confidence) Valid() bool {
	switch c {
	case "", ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return true
	}
	return false
}

// Precision qualifies an Interval. Empty means unstated.
type Precision string

const (
	PrecisionExact       Precision = "exact"
	PrecisionApproximate Precision = "approximate"
)

func (p Precision) Valid() bool {
	switch p {
	case "", PrecisionExact, PrecisionApproximate:
		return true
	}
	return false
}

// Truth is a Statement's canon truth value.
//
// It unmarshals from a YAML boolean as well as a string, because `truth: true`
// is what an author writes and making them quote it would be a papercut on the
// single most-read field of a statement.
type Truth string

const (
	TruthTrue       Truth = "true"
	TruthFalse      Truth = "false"
	TruthUnresolved Truth = "unresolved"
)

func (t Truth) Valid() bool {
	switch t {
	case TruthTrue, TruthFalse, TruthUnresolved:
		return true
	}
	return false
}

func (t *Truth) UnmarshalYAML(n *yaml.Node) error {
	// Both !!bool and !!str arrive as a scalar whose Value is already the text
	// "true", "false", or whatever was written, so one branch covers both.
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: truth must be true, false, or unresolved", n.Line)
	}
	*t = Truth(n.Value)
	return nil
}

// VisibilityKind is the player-facing axis: may the reader see this yet?
//
// It is deliberately independent of canon truth and of any agent's belief. A
// fact can be true, known to the speaker, and still a spoiler the wiki must
// hide, so this is not derivable from the belief graph and gets its own field.
type VisibilityKind string

const (
	VisibilityPublic   VisibilityKind = "public"
	VisibilitySpoiler  VisibilityKind = "spoiler"
	VisibilityInternal VisibilityKind = "internal"
)

// Visibility is the parsed form of `public`, `internal`, or `spoiler:<act>`.
//
// Raw always holds exactly what was authored, so a finding can quote it. A
// value that does not parse leaves Kind empty and is reported by the
// validator; the act is checked against the project pack's ordered act list
// there too, since the pack is not in scope here.
type Visibility struct {
	Kind VisibilityKind
	Act  string
	Raw  string
}

func (v Visibility) Valid() bool {
	switch v.Kind {
	case VisibilityPublic, VisibilityInternal:
		return true
	case VisibilitySpoiler:
		return v.Act != ""
	}
	return false
}

// String renders the authored form, so a Visibility round-trips through YAML.
func (v Visibility) String() string {
	if v.Kind == VisibilitySpoiler {
		return string(VisibilitySpoiler) + ":" + v.Act
	}
	return string(v.Kind)
}

const spoilerPrefix = string(VisibilitySpoiler) + ":"

func (v *Visibility) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: visibility must be a single value", n.Line)
	}
	v.Raw = n.Value
	v.Kind, v.Act = "", ""

	switch {
	case n.Value == string(VisibilityPublic):
		v.Kind = VisibilityPublic
	case n.Value == string(VisibilityInternal):
		v.Kind = VisibilityInternal
	case strings.HasPrefix(n.Value, spoilerPrefix):
		if act := n.Value[len(spoilerPrefix):]; act != "" {
			v.Kind, v.Act = VisibilitySpoiler, act
		}
	}
	return nil
}

func (v Visibility) MarshalYAML() (any, error) {
	if v.Kind == "" {
		return v.Raw, nil
	}
	return v.String(), nil
}

// Interval is a span on the timeline. One type serves two readings:
//
//   - on an event's date, it is a fuzzy point — it happened somewhere in here
//   - on a character's lifespan, it is a definite span — birth at Earliest,
//     death at Latest
//
// Every field is optional, which is the point: precise calendar dates are a
// commitment that is hard to walk back, and most of what a lore system answers
// is ordering. An Interval carrying only an era is still useful, because eras
// are ordered.
type Interval struct {
	Era       string    `yaml:"era,omitempty"`
	Earliest  *int      `yaml:"earliest,omitempty"`
	Latest    *int      `yaml:"latest,omitempty"`
	Precision Precision `yaml:"precision,omitempty"`
}

// Empty reports an Interval that says nothing. The validator leans on this:
// an interval with no era and no years cannot be compared, and a check that
// cannot be made must be skipped rather than guessed at.
func (iv Interval) Empty() bool {
	return iv.Era == "" && iv.Earliest == nil && iv.Latest == nil
}

// Condition is one {decision, outcome} pair.
//
// A list of them reads as AND, and there is no OR, no negation, and no
// expression language. That ceiling is the guard against the failure mode this
// whole mechanism invites: authors tagging everything conditional until nobody
// can say what is true unconditionally.
type Condition struct {
	Decision string `yaml:"decision"`
	Outcome  string `yaml:"outcome"`
}
