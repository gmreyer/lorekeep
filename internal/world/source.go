package world

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Source locates a document in the repository and maps frontmatter paths back
// to the lines that produced them.
//
// The line map is why the parser decodes each block twice: once strictly into
// the Go type and once into a yaml.Node. A validator that reports
// "relations[2].target" with no line makes a writer count list items by hand,
// and on a character with a dozen edges they will miscount.
type Source struct {
	// File is relative to the world root, with forward slashes, so findings
	// and goldens read the same on Windows and on CI.
	File string
	// Line is the first line of the frontmatter block, in the file's own
	// coordinates. It is the fallback for a path the map does not hold.
	Line int

	lines map[string]int
}

// LineOf returns the line that authored the given frontmatter path.
//
// A path the map does not hold — a field that is absent, which is exactly the
// case a "missing required field" finding reports — falls back to its nearest
// mapped ancestor, and finally to the head of the block. It never returns
// zero: a finding with no line is worse than one that is merely coarse.
func (s Source) LineOf(path string) int {
	for p := path; p != ""; p = parentPath(p) {
		if line, ok := s.lines[p]; ok {
			return line
		}
	}
	return s.Line
}

func parentPath(p string) string {
	if i := strings.LastIndexAny(p, ".["); i >= 0 {
		return p[:i]
	}
	return ""
}

// buildLineMap walks a decoded YAML document and records the line of every
// path in it. offset converts the block's coordinates into the file's: the
// number of lines that precede the frontmatter content.
//
// A mapping entry records its key's line rather than its value's, because that
// is the line a writer looks for. For the flow mappings the format encourages
// — `{ type: member_of, target: fac_x }` — the two are the same line anyway.
func buildLineMap(doc *yaml.Node, offset int) map[string]int {
	lines := make(map[string]int)
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, prefix)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, val := n.Content[i], n.Content[i+1]
				path := key.Value
				if prefix != "" {
					path = prefix + "." + key.Value
				}
				lines[path] = key.Line + offset
				walk(val, path)
			}
		case yaml.SequenceNode:
			for i, item := range n.Content {
				path := prefix + "[" + strconv.Itoa(i) + "]"
				lines[path] = item.Line + offset
				walk(item, path)
			}
		case yaml.AliasNode:
			walk(n.Alias, prefix)
		}
	}
	walk(doc, "")
	return lines
}
