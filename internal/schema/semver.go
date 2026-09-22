package schema

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// version is a strict major.minor.patch triple. Pre-release and build metadata
// are not accepted: a schema pack version is a coordinate in the promotion
// story, not a release channel, and a hand-rolled parser keeps the dependency
// list where the spec wants it.
type version struct {
	major, minor, patch int
}

func parseVersion(s string) (version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("version %q is not major.minor.patch", s)
	}
	var out [3]int
	for i, p := range parts {
		n, err := parseNumeric(p)
		if err != nil {
			return version{}, fmt.Errorf("version %q: %w", s, err)
		}
		out[i] = n
	}
	return version{major: out[0], minor: out[1], patch: out[2]}, nil
}

// parseNumeric accepts a run of digits with no sign and no leading zero, so
// "01" and "-2" are rejected rather than quietly normalised.
func parseNumeric(p string) (int, error) {
	if p == "" {
		return 0, fmt.Errorf("empty version component")
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("component %q is not a number", p)
		}
	}
	if len(p) > 1 && p[0] == '0' {
		return 0, fmt.Errorf("component %q has a leading zero", p)
	}
	return strconv.Atoi(p)
}

func (v version) compare(other version) int {
	return cmp.Or(
		cmp.Compare(v.major, other.major),
		cmp.Compare(v.minor, other.minor),
		cmp.Compare(v.patch, other.patch),
	)
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}
