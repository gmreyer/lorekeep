package world

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Change is one edit to a file's frontmatter.
//
// Path walks from the frontmatter mapping: a segment is a key in a mapping
// and a decimal index in a list. {"lifespan", "latest"} names the latest key
// of the lifespan mapping; {"relations", "1", "target"} names the target of
// the second relation, so an editor changes one item without replacing the
// list. "-" as the last segment appends an item to a list ({"relations",
// "-"}), creating the list if the key is absent. An index out of range is an
// error.
//
// Value is anything yaml.v3 can encode — a scalar, a list, a map. A value that
// replaces another, or a new list item, takes the inline (flow) style of what
// it replaces or of its first sibling, so an inline item stays inline.
//
// Delete removes the key or the list item instead. Deleting a key that is not
// there is a no-op; deleting an index out of range is an error.
type Change struct {
	Path   []string
	Value  any
	Delete bool
}

// EditFrontmatter applies changes, in order, to the frontmatter of one world
// file and returns the new file.
//
// The frontmatter is edited as a yaml.Node tree, never as a struct, so key
// order and comments survive, and a field this version of lorekeep does not
// know is carried through rather than dropped. A new key is appended at the
// end of its mapping; a missing intermediate mapping is created the same way.
//
// Everything outside the frontmatter — a BOM, the fences, the body — is
// copied byte for byte: prose is canonical for narrative, and a save that
// reflowed it would turn every edit into a diff of the whole file.
//
// When the changes leave the frontmatter semantically equal to what it was,
// src comes back unchanged with changed=false, so the caller writes nothing
// and a hand-written file keeps its formatting until someone really edits it.
// Otherwise the block is re-encoded once with 2-space indentation, which may
// re-quote scalars and re-flow multi-line flow collections; re-encoding that
// output again changes nothing. A file whose frontmatter uses CRLF gets CRLF
// throughout the new block.
//
// It never touches the filesystem; the caller writes.
func EditFrontmatter(src []byte, changes []Change) (out []byte, changed bool, err error) {
	fm, err := locateFrontmatter(src)
	if err != nil {
		return nil, false, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(fm.block, &doc); err != nil {
		return nil, false, fmt.Errorf("frontmatter: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, errors.New("frontmatter is not a mapping of fields")
	}

	var before any
	if err := doc.Decode(&before); err != nil {
		return nil, false, fmt.Errorf("frontmatter: %w", err)
	}

	for i, c := range changes {
		if len(c.Path) == 0 {
			return nil, false, fmt.Errorf("change %d: empty path", i)
		}
		if c.Delete {
			err = deletePath(doc.Content[0], c.Path)
		} else {
			err = setPath(doc.Content[0], c.Path, c.Value)
		}
		if err != nil {
			return nil, false, fmt.Errorf("change %d (%s): %w", i, strings.Join(c.Path, "."), err)
		}
	}

	var after any
	if err := doc.Decode(&after); err != nil {
		return nil, false, fmt.Errorf("frontmatter: %w", err)
	}
	if reflect.DeepEqual(before, after) {
		return src, false, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, false, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, false, fmt.Errorf("encode frontmatter: %w", err)
	}
	block := buf.Bytes()
	if fm.crlf {
		block = bytes.ReplaceAll(block, []byte("\n"), []byte("\r\n"))
	}

	out = make([]byte, 0, len(src)+len(block))
	out = append(out, src[:fm.start]...)
	out = append(out, block...)
	out = append(out, src[fm.end:]...)
	return out, true, nil
}

// rawFrontmatter locates the YAML block inside the original bytes. start and
// end bound the block's lines in src, terminators included, so that the
// opening fence, the closing fence and the body around them are spliced back
// untouched.
type rawFrontmatter struct {
	block      []byte // LF-normalised, as Parse decodes it
	start, end int
	crlf       bool
}

// locateFrontmatter finds the fences by the same rules as splitFrontmatter —
// an optional BOM, blank lines, then a --- line; the block ends at the next
// --- line — but keeps offsets into src instead of normalising it, because
// the bytes around the block must come back exactly as they were.
func locateFrontmatter(src []byte) (rawFrontmatter, error) {
	pos := 0
	if bytes.HasPrefix(src, utf8BOM) {
		pos = len(utf8BOM)
	}

	// nextLine returns the line at p without its terminator, and the offset
	// just past the terminator.
	nextLine := func(p int) (string, int, bool) {
		if p >= len(src) {
			return "", p, false
		}
		i := bytes.IndexByte(src[p:], '\n')
		if i < 0 {
			return strings.TrimSuffix(string(src[p:]), "\r"), len(src), true
		}
		return strings.TrimSuffix(string(src[p:p+i]), "\r"), p + i + 1, true
	}
	isFence := func(line string) bool { return strings.TrimRight(line, " \t") == fence }

	var fm rawFrontmatter
	opened := false
	for {
		line, next, ok := nextLine(pos)
		if !ok {
			break
		}
		if strings.TrimSpace(line) == "" {
			pos = next
			continue
		}
		if isFence(line) {
			opened = true
			fm.crlf = next >= 2 && src[next-1] == '\n' && src[next-2] == '\r'
			pos = next
		}
		break
	}
	if !opened {
		return fm, errors.New("no YAML frontmatter: the file does not open with a --- fence")
	}

	fm.start = pos
	var lines []string
	for {
		line, next, ok := nextLine(pos)
		if !ok {
			return fm, errors.New("frontmatter is never closed: no --- fence after the fields")
		}
		if isFence(line) {
			fm.end = pos
			break
		}
		lines = append(lines, line)
		pos = next
	}
	fm.block = []byte(strings.Join(lines, "\n"))
	return fm, nil
}

// lookupKey returns the index in m.Content of the value for key, or -1.
func lookupKey(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

// appendSegment, as the last segment of a path into a list, adds an item.
const appendSegment = "-"

// where names the node path[:depth] in an error.
func where(path []string, depth int) string {
	if depth == 0 {
		return "frontmatter"
	}
	return strings.Join(path[:depth], ".")
}

// itemIndex resolves a path segment against a list: a decimal index of an
// existing item.
func itemIndex(seq *yaml.Node, path []string, depth int) (int, error) {
	seg := path[depth]
	if seg == "" || strings.Trim(seg, "0123456789") != "" {
		return 0, fmt.Errorf("%s is a list: %q is not an item index", where(path, depth), seg)
	}
	i, err := strconv.Atoi(seg)
	if err != nil || i >= len(seq.Content) {
		return 0, fmt.Errorf("%s: index %s is out of range (%d items)", where(path, depth), seg, len(seq.Content))
	}
	return i, nil
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

func setPath(n *yaml.Node, path []string, value any) error {
	last := len(path) - 1
	for depth, seg := range path[:last] {
		switch n.Kind {
		case yaml.MappingNode:
			next := path[depth+1]
			i := lookupKey(n, seg)
			if i < 0 {
				if next != appendSegment && next != "" && strings.Trim(next, "0123456789") == "" {
					return fmt.Errorf("%s: index %s is out of range (no list)", where(path, depth+1), next)
				}
				n.Content = append(n.Content, keyNode(seg), emptyCollection(next))
				i = len(n.Content) - 1
			} else if isNull(n.Content[i]) {
				// `lifespan: null` is an absent collection, not a scalar in the way.
				old := n.Content[i]
				c := emptyCollection(next)
				c.HeadComment, c.LineComment, c.FootComment = old.HeadComment, old.LineComment, old.FootComment
				n.Content[i] = c
			}
			n = n.Content[i]
		case yaml.SequenceNode:
			if seg == appendSegment {
				return fmt.Errorf("%s: %q appends an item and must end the path", where(path, depth), appendSegment)
			}
			i, err := itemIndex(n, path, depth)
			if err != nil {
				return err
			}
			n = n.Content[i]
		default:
			return fmt.Errorf("%s is not a mapping or a list", where(path, depth))
		}
	}

	v := &yaml.Node{}
	if err := v.Encode(value); err != nil {
		return err
	}

	seg := path[last]
	switch n.Kind {
	case yaml.MappingNode:
		i := lookupKey(n, seg)
		if i < 0 {
			n.Content = append(n.Content, keyNode(seg), v)
			return nil
		}
		replace(n, i, v)
	case yaml.SequenceNode:
		if seg == appendSegment {
			if len(n.Content) > 0 {
				inheritStyle(v, n.Content[0])
			}
			n.Content = append(n.Content, v)
			return nil
		}
		i, err := itemIndex(n, path, last)
		if err != nil {
			return err
		}
		replace(n, i, v)
	default:
		return fmt.Errorf("%s is not a mapping or a list", where(path, last))
	}
	return nil
}

// emptyCollection is the collection a missing intermediate on a set path
// becomes: a list when the next segment appends, a mapping otherwise.
func emptyCollection(next string) *yaml.Node {
	if next == appendSegment {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// replace puts v at parent.Content[i]. The replaced value's comments move to
// v, and v takes the replaced value's style, so an inline item stays inline.
func replace(parent *yaml.Node, i int, v *yaml.Node) {
	old := parent.Content[i]
	v.HeadComment, v.LineComment, v.FootComment = old.HeadComment, old.LineComment, old.FootComment
	inheritStyle(v, old)
	parent.Content[i] = v
}

// inheritStyle gives the freshly encoded v the flow style of tmpl, the value
// it replaces or a sibling of it. A value encoded from Go is block style
// throughout; without this, editing one relation in a list of inline
// `- {type: ..., target: ...}` items would turn the edited one, or every one
// when the list is replaced whole, into a multi-line block — churn D10 does
// not intend.
//
// A flow collection makes all of v flow. A block mapping passes its style on
// key by key, and a block list passes its first item's style to every item.
func inheritStyle(v, tmpl *yaml.Node) {
	if tmpl == nil || v.Kind != tmpl.Kind {
		return
	}
	switch v.Kind {
	case yaml.MappingNode:
		if tmpl.Style&yaml.FlowStyle != 0 {
			setFlow(v)
			return
		}
		for i := 0; i+1 < len(v.Content); i += 2 {
			if j := lookupKey(tmpl, v.Content[i].Value); j >= 0 {
				inheritStyle(v.Content[i+1], tmpl.Content[j])
			}
		}
	case yaml.SequenceNode:
		if tmpl.Style&yaml.FlowStyle != 0 {
			setFlow(v)
			return
		}
		if len(tmpl.Content) > 0 {
			for _, item := range v.Content {
				inheritStyle(item, tmpl.Content[0])
			}
		}
	}
}

func setFlow(n *yaml.Node) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style |= yaml.FlowStyle
	}
	for _, c := range n.Content {
		setFlow(c)
	}
}

func deletePath(n *yaml.Node, path []string) error {
	last := len(path) - 1
	for depth, seg := range path {
		switch n.Kind {
		case yaml.MappingNode:
			i := lookupKey(n, seg)
			if i < 0 {
				return nil
			}
			if depth == last {
				n.Content = append(n.Content[:i-1], n.Content[i+1:]...)
				return nil
			}
			n = n.Content[i]
		case yaml.SequenceNode:
			i, err := itemIndex(n, path, depth)
			if err != nil {
				return err
			}
			if depth == last {
				n.Content = append(n.Content[:i], n.Content[i+1:]...)
				return nil
			}
			n = n.Content[i]
		default:
			if isNull(n) {
				return nil
			}
			return fmt.Errorf("%s is not a mapping or a list", where(path, depth))
		}
	}
	return nil
}

func keyNode(key string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
}
