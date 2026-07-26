package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Normalize canonicalises a rendered manifest stream so two renders can be
// compared meaningfully.
//
// Documents are sorted by identity — apiVersion, kind, namespace, name — because
// kustomize's output order is not guaranteed stable across edits, and a
// reordered document would otherwise show up as a large deletion followed by a
// large insertion. Each document is re-encoded through yaml.Node rather than a
// map, which preserves the key order as authored: decoding into map[string]any
// would reorder every key on every render and bury the one line that changed.
func Normalize(manifests []byte) ([]byte, error) {
	docs, err := splitDocuments(manifests)
	if err != nil {
		return nil, err
	}

	// A stable sort keeps documents that share an identity — which is invalid
	// input, but not this function's business to reject — in input order.
	stableSortBy(docs, func(a, b document) bool { return a.key < b.key })

	var out bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			out.WriteString("---\n")
		}
		out.Write(doc.text)
	}
	return out.Bytes(), nil
}

type document struct {
	key  string
	text []byte
}

func splitDocuments(manifests []byte) ([]document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(manifests))
	var docs []document
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse rendered manifests: %w", err)
		}
		// An empty document (a stray "---") decodes to a zero node.
		if node.Kind == 0 {
			continue
		}

		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&node); err != nil {
			return nil, fmt.Errorf("re-encode rendered manifest: %w", err)
		}
		enc.Close()

		docs = append(docs, document{key: identityOf(&node), text: buf.Bytes()})
	}
	return docs, nil
}

// identityOf builds the sort key from a resource's identity fields. Missing
// fields sort first, which groups malformed documents together rather than
// scattering them.
func identityOf(node *yaml.Node) string {
	root := node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	apiVersion := mappingValue(root, "apiVersion")
	kind := mappingValue(root, "kind")
	metadata := mappingNode(root, "metadata")
	namespace := mappingValue(metadata, "namespace")
	name := mappingValue(metadata, "name")
	return strings.Join([]string{apiVersion, kind, namespace, name}, "\x00")
}

func mappingNode(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mappingValue(node *yaml.Node, key string) string {
	if v := mappingNode(node, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

// stableSortBy is a small insertion sort: document counts here are in the tens,
// and this keeps the comparison inline and obvious.
func stableSortBy(docs []document, less func(a, b document) bool) {
	for i := 1; i < len(docs); i++ {
		for j := i; j > 0 && less(docs[j], docs[j-1]); j-- {
			docs[j], docs[j-1] = docs[j-1], docs[j]
		}
	}
}

// Palette holds the ANSI codes used by Diff.
type Palette struct {
	Add, Del, Hunk, Reset string
}

// ColorPalette and PlainPalette are the two available renderings.
var (
	ColorPalette = Palette{Add: "\x1b[32m", Del: "\x1b[31m", Hunk: "\x1b[36m", Reset: "\x1b[0m"}
	PlainPalette = Palette{}
)

const diffContext = 3

// Diff renders a unified diff of two normalised manifest streams. An empty
// result means the renders are identical.
func Diff(before, after []byte, fromLabel, toLabel string, p Palette) string {
	a := splitLines(before)
	b := splitLines(after)
	hunks := diffHunks(a, b)
	if len(hunks) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", fromLabel, toLabel)
	for _, h := range hunks {
		fmt.Fprintf(&out, "%s@@ -%d,%d +%d,%d @@%s\n", p.Hunk, h.aStart+1, h.aLen, h.bStart+1, h.bLen, p.Reset)
		for _, line := range h.lines {
			switch line[0] {
			case '+':
				fmt.Fprintf(&out, "%s%s%s\n", p.Add, line, p.Reset)
			case '-':
				fmt.Fprintf(&out, "%s%s%s\n", p.Del, line, p.Reset)
			default:
				fmt.Fprintf(&out, "%s\n", line)
			}
		}
	}
	return out.String()
}

func splitLines(b []byte) []string {
	s := string(b)
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type hunk struct {
	aStart, aLen int
	bStart, bLen int
	lines        []string
}

// diffHunks produces unified-diff hunks from a longest-common-subsequence
// table. Manifest renders for one component are hundreds of lines, so the
// quadratic table is cheap and the implementation stays readable.
func diffHunks(a, b []string) []hunk {
	lcs := lcsTable(a, b)

	// Walk the table to build the full edit script first, then group it.
	type edit struct {
		op   byte // ' ', '-', '+'
		text string
	}
	var script []edit
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			script = append(script, edit{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			script = append(script, edit{'-', a[i]})
			i++
		default:
			script = append(script, edit{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		script = append(script, edit{'-', a[i]})
	}
	for ; j < len(b); j++ {
		script = append(script, edit{'+', b[j]})
	}

	// Collect the positions of the changed edits.
	var changedAt []int
	for k, e := range script {
		if e.op != ' ' {
			changedAt = append(changedAt, k)
		}
	}
	if len(changedAt) == 0 {
		return nil
	}

	// Record each edit's line number on both sides, for the hunk headers.
	lineNums := make([][2]int, len(script))
	aLine, bLine := 0, 0
	for k, e := range script {
		lineNums[k] = [2]int{aLine, bLine}
		switch e.op {
		case ' ':
			aLine++
			bLine++
		case '-':
			aLine++
		case '+':
			bLine++
		}
	}

	// Group changes whose gap is small enough that their context windows would
	// overlap, so nearby edits share one hunk instead of repeating context.
	type group struct{ first, last int }
	groups := []group{{changedAt[0], changedAt[0]}}
	for _, k := range changedAt[1:] {
		if last := &groups[len(groups)-1]; k-last.last > 2*diffContext {
			groups = append(groups, group{k, k})
		} else {
			last.last = k
		}
	}

	hunks := make([]hunk, 0, len(groups))
	for _, g := range groups {
		start := max(0, g.first-diffContext)
		stop := min(len(script), g.last+diffContext+1)

		h := hunk{aStart: lineNums[start][0], bStart: lineNums[start][1]}
		for _, e := range script[start:stop] {
			h.lines = append(h.lines, string(e.op)+e.text)
			switch e.op {
			case ' ':
				h.aLen++
				h.bLen++
			case '-':
				h.aLen++
			case '+':
				h.bLen++
			}
		}
		hunks = append(hunks, h)
	}
	return hunks
}

func lcsTable(a, b []string) [][]int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	return table
}
