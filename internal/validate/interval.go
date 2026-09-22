package validate

import (
	"fmt"
	"strings"

	"github.com/gmreyer/lore-core/internal/world"
)

// The time model is eras plus optional fuzzy years, so comparison is coarse on
// purpose. Precise calendar dates are a commitment that is hard to walk back,
// and most of what this system answers is ordering.
//
// Everything below is deliberately conservative: where the authored data
// cannot settle a question, the answer is "no warning". A false continuity
// warning costs a writer more than a missed one, because the missed one is
// caught on the next read and the false one teaches them to stop reading.

// bound is a position on the timeline: an era, and a year within it if one was
// written down.
type bound struct {
	era     int
	year    int
	hasYear bool
}

// compare orders two bounds. Two bounds in the same era where either has no
// year compare equal, because "somewhere in the third reign" cannot be placed
// against "412 of the third reign" without inventing a fact.
func (b bound) compare(other bound) int {
	switch {
	case b.era != other.era:
		return b.era - other.era
	case !b.hasYear || !other.hasYear:
		return 0
	case b.year != other.year:
		return b.year - other.year
	}
	return 0
}

// span is an interval resolved against the pack's era order.
type span struct {
	lo, hi bound
	text   string
}

func (s span) String() string { return s.text }

// excludes reports whether other falls entirely outside s, and says which way.
func (s span) excludes(other span) (reason string, outside bool) {
	if other.hi.compare(s.lo) < 0 {
		return "entirely before they were born", true
	}
	if other.lo.compare(s.hi) > 0 {
		return "entirely after they died", true
	}
	return "", false
}

// span resolves an interval into something comparable.
//
// It refuses unless the interval names an era the pack declares. Years alone
// are not enough: two years in unnamed eras are not on the same axis, and
// comparing them would be a guess dressed as a check.
func (v *validator) span(iv *world.Interval) (span, bool) {
	if iv == nil || iv.Era == "" {
		return span{}, false
	}
	era, ok := v.pack.EraOrdinal(iv.Era)
	if !ok {
		return span{}, false
	}

	s := span{
		lo:   bound{era: era},
		hi:   bound{era: era},
		text: describe(iv),
	}
	if iv.Earliest != nil {
		s.lo = bound{era: era, year: *iv.Earliest, hasYear: true}
	}
	if iv.Latest != nil {
		s.hi = bound{era: era, year: *iv.Latest, hasYear: true}
	}
	return s, true
}

func describe(iv *world.Interval) string {
	var b strings.Builder
	b.WriteString(iv.Era)
	switch {
	case iv.Earliest != nil && iv.Latest != nil:
		fmt.Fprintf(&b, " %d-%d", *iv.Earliest, *iv.Latest)
	case iv.Earliest != nil:
		fmt.Fprintf(&b, " from %d", *iv.Earliest)
	case iv.Latest != nil:
		fmt.Fprintf(&b, " until %d", *iv.Latest)
	}
	if iv.Precision == world.PrecisionApproximate {
		b.WriteString(" (approximate)")
	}
	return b.String()
}
