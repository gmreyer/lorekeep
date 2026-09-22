package world

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document is one parsed world file. Exactly one of Entity and Statement is
// set, decided by the frontmatter's `type` field and by nothing else.
type Document struct {
	File      string
	Kind      Kind
	Entity    *Entity
	Statement *Statement
}

// fence is the frontmatter delimiter. A file may begin with a BOM and blank
// lines — an editor on Windows produces both — but nothing else.
const fence = "---"

// utf8BOM is written by more than one Windows editor and must not turn a
// perfectly good lore file into a parse error.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Parse reads one world file. It returns whatever it managed to decode
// alongside every fault it found, because reporting a whole file in one pass
// is worth more to a writer than stopping at the first mistake.
//
// name is the path used in findings: relative to the world root, forward
// slashes.
func Parse(name string, data []byte) (Document, []Finding) {
	doc := Document{File: name}

	block, body, offset, f := splitFrontmatter(name, data)
	if f != nil {
		return doc, []Finding{*f}
	}

	src := Source{File: name, Line: offset + 1}

	// Decoded twice on purpose. The strict pass rejects unknown fields, so a
	// typo fails the build rather than vanishing; the node pass builds the
	// line map, which strict decoding cannot give us.
	var node yaml.Node
	if err := yaml.Unmarshal(block, &node); err == nil {
		src.lines = buildLineMap(&node, offset)
	}

	// The type field routes the document. It is read loosely, without strict
	// field checking, so that a file with an unrelated typo still lands in the
	// right loader and reports its real fault.
	var head struct {
		Type string `yaml:"type"`
	}
	_ = yaml.Unmarshal(block, &head)

	if head.Type == StatementType {
		doc.Kind = KindStatement
		s := &Statement{}
		findings, ok := decodeStrict(name, block, offset, s)
		if ok {
			s.Body, s.Source = body, src
			doc.Statement = s
		}
		return doc, findings
	}

	doc.Kind = KindEntity
	e := &Entity{}
	findings, ok := decodeStrict(name, block, offset, e)
	if ok {
		e.Body, e.Source = body, src
		doc.Entity = e
	}
	return doc, findings
}

// splitFrontmatter separates the fenced YAML block from the prose body and
// reports how many lines precede the block, so that every line the parser
// reports is in the file's own coordinates rather than the block's.
func splitFrontmatter(name string, data []byte) (block []byte, body string, offset int, f *Finding) {
	text := strings.ReplaceAll(string(bytes.TrimPrefix(data, utf8BOM)), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimRight(line, " \t") == fence {
			start = i
		}
		break
	}
	if start < 0 {
		return nil, "", 0, &Finding{
			Code: CodeNoFrontmatter, Severity: SeverityError, File: name, Line: 1,
			Msg: "no YAML frontmatter: a world file opens with a --- fence, and the graph is read from frontmatter only",
		}
	}

	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == fence {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", 0, &Finding{
			Code: CodeUnterminatedFrontmatter, Severity: SeverityError, File: name, Line: start + 1,
			Msg: "frontmatter is never closed: add a --- fence where the fields end and the prose begins",
		}
	}

	block = []byte(strings.Join(lines[start+1:end], "\n"))
	if strings.TrimSpace(string(block)) == "" {
		return nil, "", 0, &Finding{
			Code: CodeEmptyFrontmatter, Severity: SeverityError, File: name, Line: start + 1,
			Msg: "frontmatter is empty: a world file must at least declare an id and a type",
		}
	}

	body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return block, body, start + 1, nil
}

// decodeStrict decodes the block with unknown fields rejected, translating
// yaml.v3's faults into located findings.
//
// The bool reports whether out is usable. A type error — an unknown field, a
// number where a string belongs — still leaves everything else decoded, so the
// document is kept and the rest of it is validated. A syntax error leaves
// nothing behind, and handing an empty struct on to the validator would bury
// the real fault under a page of invented ones.
func decodeStrict(name string, block []byte, offset int, out any) ([]Finding, bool) {
	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	err := dec.Decode(out)
	if err == nil || errors.Is(err, io.EOF) {
		return nil, true
	}

	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		findings := make([]Finding, 0, len(typeErr.Errors))
		for _, msg := range typeErr.Errors {
			line, text := splitYAMLLine(msg)
			findings = append(findings, Finding{
				Code:     codeForYAMLMessage(text),
				Severity: SeverityError,
				File:     name,
				Line:     line + offset,
				Msg:      text,
			})
		}
		return findings, true
	}

	line, text := splitYAMLLine(err.Error())
	return []Finding{{
		Code: CodeParse, Severity: SeverityError, File: name,
		Line: line + offset, Msg: text,
	}}, false
}

// yamlLine matches the "line N: " prefix yaml.v3 puts on its messages. Pulling
// the number out is the only way to locate a decode fault, since the decoder
// hands back text rather than a node.
var yamlLine = regexp.MustCompile(`^(?:yaml: )?line (\d+): `)

func splitYAMLLine(msg string) (int, string) {
	msg = strings.TrimPrefix(msg, "yaml: ")
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			return n, strings.TrimPrefix(msg, m[0])
		}
	}
	return 1, msg
}

func codeForYAMLMessage(msg string) Code {
	if strings.Contains(msg, "not found in type") {
		return CodeUnknownField
	}
	return CodeParse
}
