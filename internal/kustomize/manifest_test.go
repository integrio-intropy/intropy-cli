package kustomize

import (
	"strings"
	"testing"
)

const twoDocsInOrder = `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: integrations
data:
  key: value
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-extractor
  namespace: integrations
spec:
  replicas: 1
`

const twoDocsReversed = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-extractor
  namespace: integrations
spec:
  replicas: 1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: integrations
data:
  key: value
`

// kustomize's output order is not guaranteed stable across edits. Without
// sorting, a reordered document reads as a large deletion plus a large
// insertion and buries the line that actually changed.
func TestNormalizeSortsDocumentsByIdentity(t *testing.T) {
	forward, err := Normalize([]byte(twoDocsInOrder))
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := Normalize([]byte(twoDocsReversed))
	if err != nil {
		t.Fatal(err)
	}

	if string(forward) != string(reversed) {
		t.Errorf("reordered documents should normalise identically:\n--- forward\n%s\n--- reversed\n%s", forward, reversed)
	}
	if Diff(forward, reversed, "before", "after", PlainPalette) != "" {
		t.Error("reordering alone should produce no diff")
	}
}

// Decoding into a map would reorder every key on every render. The whole point
// of going through yaml.Node is that the one line that changed stays visible.
func TestNormalizePreservesKeyOrder(t *testing.T) {
	out, err := Normalize([]byte(twoDocsInOrder))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)

	apiVersion := strings.Index(text, "apiVersion:")
	kind := strings.Index(text, "kind:")
	metadata := strings.Index(text, "metadata:")
	if !(apiVersion < kind && kind < metadata) {
		t.Errorf("keys were reordered; got:\n%s", text)
	}
}

func TestNormalizeHandlesEmptyAndStraySeparators(t *testing.T) {
	out, err := Normalize([]byte("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "kind:") != 1 {
		t.Errorf("expected exactly one document, got:\n%s", out)
	}
	if strings.Contains(string(out), "---\n---") {
		t.Errorf("stray separators should be dropped, got:\n%s", out)
	}
}

func TestNormalizeRejectsMalformedYAML(t *testing.T) {
	if _, err := Normalize([]byte("apiVersion: [unclosed\n")); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

// The two documents above, as ArgoCD's render endpoint returns them: one JSON
// document per resource, each on a single line.
var renderedJSON = []string{
	`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings","namespace":"integrations"},"data":{"key":"value"}}`,
	`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"order-extractor","namespace":"integrations"},"spec":{"replicas":1}}`,
}

// JSON is valid YAML, but yaml.v3 remembers the flow style it decoded and would
// re-encode each resource onto one line — unreadable, and a one-line diff for any
// change anywhere in a resource.
func TestNormalizeJSONEmitsBlockYAML(t *testing.T) {
	out, err := NormalizeJSON(renderedJSON)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)

	if strings.Contains(text, `{"apiVersion"`) || strings.Contains(text, "{apiVersion:") {
		t.Errorf("output should be block YAML, not flow style:\n%s", text)
	}
	if !strings.Contains(text, "metadata:\n  name: settings") {
		t.Errorf("nested mappings should be indented block style:\n%s", text)
	}
	// Key order is the order the JSON carried, for the same reason Normalize
	// preserves the authored order.
	apiVersion := strings.Index(text, "apiVersion:")
	kind := strings.Index(text, "kind:")
	if !(apiVersion < kind) {
		t.Errorf("keys were reordered:\n%s", text)
	}
}

// Both sides of a review may be rendered by ArgoCD or read from a kustomize
// build, so the two entry points have to agree on document order and shape.
func TestNormalizeJSONMatchesNormalize(t *testing.T) {
	fromJSON, err := NormalizeJSON(renderedJSON)
	if err != nil {
		t.Fatal(err)
	}
	fromYAML, err := Normalize([]byte(twoDocsInOrder))
	if err != nil {
		t.Fatal(err)
	}
	if string(fromJSON) != string(fromYAML) {
		t.Errorf("the same resources should normalise identically:\n--- from JSON\n%s\n--- from YAML\n%s", fromJSON, fromYAML)
	}
}

func TestNormalizeJSONHandlesNoDocuments(t *testing.T) {
	out, err := NormalizeJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("nothing rendered should normalise to nothing, got %q", out)
	}
}

// A caller comparing two renders reports the difference to someone, so the
// identity has to be both unique and readable.
func TestIdentitiesNamesResourcesTheWayKubectlDoes(t *testing.T) {
	normalized, err := Normalize([]byte(twoDocsInOrder))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := Identities(normalized)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Deployment integrations/order-extractor", "ConfigMap integrations/settings"}
	if len(ids) != len(want) {
		t.Fatalf("identities = %v, want %v", ids, want)
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("identities[%d] = %q, want %q", i, ids[i], w)
		}
	}
}

// A cluster-scoped resource has no namespace to qualify it.
func TestIdentitiesOmitsAnAbsentNamespace(t *testing.T) {
	ids, err := Identities([]byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: integrations\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "Namespace integrations" {
		t.Errorf("identities = %v, want [\"Namespace integrations\"]", ids)
	}
}

func TestDiffIdenticalIsEmpty(t *testing.T) {
	a, err := Normalize([]byte(twoDocsInOrder))
	if err != nil {
		t.Fatal(err)
	}
	if got := Diff(a, a, "before", "after", PlainPalette); got != "" {
		t.Errorf("Diff of identical input = %q, want empty", got)
	}
}

func TestDiffShowsChangedLineWithContext(t *testing.T) {
	// The change is on line 6, so with three lines of context the hunk spans
	// lines 3 to 9 and both "a" and "k" fall outside it.
	before := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n")
	after := []byte("a\nb\nc\nd\ne\nCHANGED\ng\nh\ni\nj\nk\n")

	got := Diff(before, after, "before", "after", PlainPalette)
	if got == "" {
		t.Fatal("expected a diff")
	}
	if !strings.Contains(got, "-f") || !strings.Contains(got, "+CHANGED") {
		t.Errorf("diff should show the replaced line:\n%s", got)
	}
	if !strings.Contains(got, "--- before") || !strings.Contains(got, "+++ after") {
		t.Errorf("diff should carry file labels:\n%s", got)
	}
	if !strings.Contains(got, "@@ -3,7 +3,7 @@") {
		t.Errorf("hunk header should locate the change:\n%s", got)
	}
	// Three lines of context on each side, and nothing beyond them. Compared
	// line by line: substring matching would hit the "--- before" header, which
	// contains " b".
	lines := diffLineSet(got)
	for _, want := range []string{" c", " d", " e", " g", " h", " i"} {
		if !lines[want] {
			t.Errorf("diff should include context line %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{" a", " b", " j", " k"} {
		if lines[unwanted] {
			t.Errorf("diff should not include %q, beyond the context window:\n%s", unwanted, got)
		}
	}
}

// diffLineSet indexes a diff's lines so tests can assert on whole lines.
func diffLineSet(diff string) map[string]bool {
	out := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSuffix(diff, "\n"), "\n") {
		out[line] = true
	}
	return out
}

func TestDiffAdditionsAndDeletions(t *testing.T) {
	if got := Diff([]byte("a\n"), []byte("a\nb\n"), "b", "a", PlainPalette); !strings.Contains(got, "+b") {
		t.Errorf("addition not shown:\n%s", got)
	}
	if got := Diff([]byte("a\nb\n"), []byte("a\n"), "b", "a", PlainPalette); !strings.Contains(got, "-b") {
		t.Errorf("deletion not shown:\n%s", got)
	}
	if got := Diff(nil, []byte("a\n"), "b", "a", PlainPalette); !strings.Contains(got, "+a") {
		t.Errorf("diff from empty not shown:\n%s", got)
	}
	if got := Diff([]byte("a\n"), nil, "b", "a", PlainPalette); !strings.Contains(got, "-a") {
		t.Errorf("diff to empty not shown:\n%s", got)
	}
}

// Distant changes must not be merged into one hunk with a huge context gap.
func TestDiffSeparatesDistantChanges(t *testing.T) {
	var before, after []string
	for i := range 40 {
		before = append(before, string(rune('a'+i%26))+strings.Repeat("x", i))
		after = append(after, string(rune('a'+i%26))+strings.Repeat("x", i))
	}
	after[2] = "FIRST"
	after[35] = "SECOND"

	got := Diff([]byte(strings.Join(before, "\n")+"\n"), []byte(strings.Join(after, "\n")+"\n"), "b", "a", PlainPalette)
	if n := countHunks(got); n != 2 {
		t.Errorf("expected 2 hunks, got %d:\n%s", n, got)
	}
}

// Nearby changes should share a hunk rather than repeat overlapping context.
func TestDiffMergesNearbyChanges(t *testing.T) {
	before := []byte("a\nb\nc\nd\ne\nf\ng\n")
	after := []byte("a\nB\nc\nD\ne\nf\ng\n")

	got := Diff(before, after, "b", "a", PlainPalette)
	if n := countHunks(got); n != 1 {
		t.Errorf("expected 1 merged hunk, got %d:\n%s", n, got)
	}
}

func TestDiffColour(t *testing.T) {
	before := []byte("a\nb\n")
	after := []byte("a\nc\n")

	plain := Diff(before, after, "b", "a", PlainPalette)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("plain palette should emit no escape codes: %q", plain)
	}

	coloured := Diff(before, after, "b", "a", ColorPalette)
	if !strings.Contains(coloured, ColorPalette.Add) || !strings.Contains(coloured, ColorPalette.Del) {
		t.Errorf("colour palette should colour additions and deletions: %q", coloured)
	}
	if !strings.HasSuffix(strings.TrimSuffix(coloured, "\n"), ColorPalette.Reset) {
		t.Errorf("coloured output should reset at the end: %q", coloured)
	}
}

// The realistic case this all exists for: one image line changing from a tag to
// a digest should produce a small, readable diff.
func TestDiffOfADigestPinIsSmall(t *testing.T) {
	before := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-extractor
spec:
  template:
    spec:
      containers:
      - image: harbor.intropy.io/integrations/order-extractor:latest
        name: app
`)
	after := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-extractor
spec:
  template:
    spec:
      containers:
      - image: harbor.intropy.io/integrations/order-extractor@sha256:abc123
        name: app
`)

	got := Diff(before, after, "before", "after", PlainPalette)
	added := strings.Count(got, "\n+") + boolToInt(strings.HasPrefix(got, "+"))
	removed := strings.Count(got, "\n-") + boolToInt(strings.HasPrefix(got, "-"))
	// One +++ header line counts as an addition prefix, so allow for it.
	if added > 3 || removed > 3 {
		t.Errorf("a single image change should be a small diff, got %d additions and %d removals:\n%s", added, removed, got)
	}
	if !strings.Contains(got, "sha256:abc123") {
		t.Errorf("diff should show the new digest:\n%s", got)
	}
}

// countHunks counts hunk headers. Counting occurrences of "@@" would double
// every header, since a unified-diff header is delimited on both sides.
func countHunks(diff string) int {
	n := 0
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			n++
		}
	}
	return n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
