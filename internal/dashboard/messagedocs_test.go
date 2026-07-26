package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fullDoc = `---
format: csv
delimiter: ";"
encoding: iso-8859-1
filePattern: "ORDERS_*.csv"
frequency: nightly ~02:00 CET
fields:
  - name: ORDER_ID
    position: 1
    type: string
    required: true
    notes: PIM order key, zero-padded 10
  - name: QTY
    type: integer
sample:
  inline: "1000000042;C1234;20260725;3"
  redacted: true
contact: PIM ops
lastReviewed: "2026-07-26"
---
Nightly FULL snapshot, not a delta.
`

func writeMessages(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	msgs := filepath.Join(dir, messagesDirName)
	if err := os.MkdirAll(msgs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(msgs, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadMessageDocsFull(t *testing.T) {
	dir := writeMessages(t, map[string]string{"order-extractor-source.md": fullDoc})
	docs, errs := readMessageDocs(dir)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	doc, ok := docs["order-extractor-source"]
	if !ok {
		t.Fatalf("docs = %v, want key order-extractor-source", docs)
	}
	if doc.Format != "csv" || doc.Delimiter != ";" || doc.Encoding != "iso-8859-1" ||
		doc.FilePattern != "ORDERS_*.csv" || doc.Frequency != "nightly ~02:00 CET" ||
		doc.Contact != "PIM ops" || doc.LastReviewed != "2026-07-26" {
		t.Errorf("meta = %+v", doc)
	}
	if len(doc.Fields) != 2 {
		t.Fatalf("fields = %+v, want 2", doc.Fields)
	}
	if f := doc.Fields[0]; f.Name != "ORDER_ID" || f.Position != 1 || f.Type != "string" ||
		!f.Required || !strings.Contains(f.Notes, "zero-padded") {
		t.Errorf("field = %+v", f)
	}
	if doc.Sample == nil || doc.Sample.Inline == "" || doc.Sample.Redacted == nil || !*doc.Sample.Redacted {
		t.Errorf("sample = %+v", doc.Sample)
	}
	if want := "Nightly FULL snapshot, not a delta."; doc.Body != want {
		t.Errorf("body = %q, want %q", doc.Body, want)
	}
}

// A prose-only file (no frontmatter) is a valid description.
func TestReadMessageDocsProseOnly(t *testing.T) {
	dir := writeMessages(t, map[string]string{"erp.md": "Just some notes about the feed.\n"})
	docs, errs := readMessageDocs(dir)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if doc := docs["erp"]; doc.Body != "Just some notes about the feed." || doc.Format != "" {
		t.Errorf("doc = %+v", doc)
	}
}

// A sample without an explicit redacted assertion is dropped, reported, and
// the rest of the doc survives.
func TestReadMessageDocsSampleRequiresRedacted(t *testing.T) {
	dir := writeMessages(t, map[string]string{"erp.md": "---\nformat: csv\nsample:\n  inline: \"a;b\"\n---\nprose\n"})
	docs, errs := readMessageDocs(dir)
	if len(errs) != 1 || !strings.Contains(errs[0], "erp.md") || !strings.Contains(errs[0], "redacted") {
		t.Fatalf("errs = %v, want one redacted error naming the file", errs)
	}
	if doc := docs["erp"]; doc.Sample != nil || doc.Format != "csv" || doc.Body != "prose" {
		t.Errorf("doc = %+v", doc)
	}
}

// Broken frontmatter rejects that doc with an error; other docs are kept.
func TestReadMessageDocsInvalidFrontmatter(t *testing.T) {
	dir := writeMessages(t, map[string]string{
		"bad.md":  "---\nformat: [unclosed\n---\nbody\n",
		"good.md": "fine\n",
	})
	docs, errs := readMessageDocs(dir)
	if len(errs) != 1 || !strings.Contains(errs[0], "messages/bad.md") {
		t.Fatalf("errs = %v, want one error for messages/bad.md", errs)
	}
	if _, ok := docs["bad"]; ok {
		t.Error("bad doc should be rejected")
	}
	if _, ok := docs["good"]; !ok {
		t.Error("good doc should survive")
	}
}

// No messages directory means no docs and no errors.
func TestReadMessageDocsAbsentDir(t *testing.T) {
	docs, errs := readMessageDocs(t.TempDir())
	if docs != nil || errs != nil {
		t.Errorf("docs = %v, errs = %v, want nil/nil", docs, errs)
	}
}

// Non-Markdown files and subdirectories are ignored.
func TestReadMessageDocsIgnoresOtherEntries(t *testing.T) {
	dir := writeMessages(t, map[string]string{"sample.csv": "a;b", "erp.md": "notes"})
	if err := os.MkdirAll(filepath.Join(dir, messagesDirName, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	docs, errs := readMessageDocs(dir)
	if len(errs) != 0 || len(docs) != 1 {
		t.Fatalf("docs = %v, errs = %v, want only erp", docs, errs)
	}
}

// An over-long body is truncated, never an error.
func TestReadMessageDocsBodyCap(t *testing.T) {
	dir := writeMessages(t, map[string]string{"erp.md": strings.Repeat("x", maxMessageBodyBytes+100)})
	docs, errs := readMessageDocs(dir)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if got := len(docs["erp"].Body); got != maxMessageBodyBytes {
		t.Errorf("body length = %d, want %d", got, maxMessageBodyBytes)
	}
}

// A doc with an opening delimiter but no closing one is all body.
func TestSplitFrontmatterUnclosed(t *testing.T) {
	meta, body := splitFrontmatter([]byte("---\nformat: csv\nno closing"))
	if meta != nil || !strings.Contains(string(body), "format: csv") {
		t.Errorf("meta = %q, body = %q", meta, body)
	}
}

// CRLF files parse the same as LF files.
func TestSplitFrontmatterCRLF(t *testing.T) {
	meta, body := splitFrontmatter([]byte("---\r\nformat: csv\r\n---\r\nprose\r\n"))
	if !strings.Contains(string(meta), "format: csv") || strings.TrimSpace(string(body)) != "prose" {
		t.Errorf("meta = %q, body = %q", meta, body)
	}
}
