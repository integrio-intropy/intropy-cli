package dashboard

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// messagesDirName is the per-system directory holding authored message
// descriptions: one Markdown file per port, named after it
// (messages/<port>.md), with YAML frontmatter for the structured facts
// and free prose in the body.
const messagesDirName = "messages"

// maxMessageBodyBytes caps the prose body served for one message description.
const maxMessageBodyBytes = 16 * 1024

// messageDoc is an authored description of the payload a port carries —
// the flat file from an SFTP drop, the ad-hoc CSV export — written by a
// developer because no machine spec exists. It is documentation, not
// enforcement: the runtime never validates against it. Contact and
// LastReviewed are its staleness story.
type messageDoc struct {
	Format       string     `json:"format,omitempty" yaml:"format"`
	Delimiter    string     `json:"delimiter,omitempty" yaml:"delimiter"`
	Encoding     string     `json:"encoding,omitempty" yaml:"encoding"`
	FilePattern  string     `json:"filePattern,omitempty" yaml:"filePattern"`
	Frequency    string     `json:"frequency,omitempty" yaml:"frequency"`
	Contact      string     `json:"contact,omitempty" yaml:"contact"`
	LastReviewed string     `json:"lastReviewed,omitempty" yaml:"lastReviewed"`
	Fields       []docField `json:"fields,omitempty" yaml:"fields"`
	Sample       *docSample `json:"sample,omitempty" yaml:"sample"`
	Body         string     `json:"body,omitempty" yaml:"-"`
}

// docField describes one field or column of an external payload. Type is
// loose by design (string/integer/decimal/date/boolean/other) — it records
// what the author knows, not a validatable schema.
type docField struct {
	Name     string `json:"name" yaml:"name"`
	Position int    `json:"position,omitempty" yaml:"position"`
	Type     string `json:"type,omitempty" yaml:"type"`
	Required bool   `json:"required,omitempty" yaml:"required"`
	Notes    string `json:"notes,omitempty" yaml:"notes"`
}

// docSample is a short inline payload excerpt. Redacted is a pointer so an
// author cannot publish a sample without explicitly asserting they considered
// what it exposes — an absent value rejects the sample, not defaults it.
type docSample struct {
	Inline   string `json:"inline" yaml:"inline"`
	Redacted *bool  `json:"redacted" yaml:"redacted"`
}

// readMessageDocs collects the authored message descriptions under a system
// directory, keyed by port name (the filename). Problems are returned as
// strings for the report's error banner — a broken doc never fails the
// request, and error paths are relative to dir so callers can prefix the
// system's identifier.
func readMessageDocs(dir string) (map[string]messageDoc, []string) {
	ents, err := os.ReadDir(filepath.Join(dir, messagesDirName))
	if err != nil {
		return nil, nil
	}
	var docs map[string]messageDoc
	var errs []string
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		rel := path.Join(messagesDirName, ent.Name())
		data, err := os.ReadFile(filepath.Join(dir, messagesDirName, ent.Name()))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		doc, docErrs := parseMessageDoc(data)
		for _, e := range docErrs {
			errs = append(errs, fmt.Sprintf("%s: %s", rel, e))
		}
		if doc == nil {
			continue
		}
		if docs == nil {
			docs = map[string]messageDoc{}
		}
		docs[strings.TrimSuffix(ent.Name(), ".md")] = *doc
	}
	return docs, errs
}

// parseMessageDoc splits a doc into YAML frontmatter and prose body. A file
// without frontmatter is a valid prose-only description. A doc is only
// rejected (nil) when its frontmatter does not parse; lesser problems drop
// the offending part and report it.
func parseMessageDoc(data []byte) (*messageDoc, []string) {
	meta, body := splitFrontmatter(data)
	var doc messageDoc
	if len(meta) > 0 {
		if err := yaml.Unmarshal(meta, &doc); err != nil {
			return nil, []string{fmt.Sprintf("invalid frontmatter: %v", err)}
		}
	}
	var errs []string
	if doc.Sample != nil && doc.Sample.Redacted == nil {
		doc.Sample = nil
		errs = append(errs, "sample dropped: set redacted: true|false to assert the sample was checked for sensitive data")
	}
	if len(body) > maxMessageBodyBytes {
		body = body[:maxMessageBodyBytes]
	}
	doc.Body = strings.TrimSpace(string(body))
	return &doc, errs
}

// splitFrontmatter separates a leading YAML frontmatter block (delimited by
// "---" lines) from the document body. Without an opening delimiter on the
// first line the whole input is body.
func splitFrontmatter(data []byte) (meta, body []byte) {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(normalized, []byte("---\n"))
	if !ok {
		return nil, normalized
	}
	if meta, body, ok = bytes.Cut(rest, []byte("\n---\n")); ok {
		return meta, body
	}
	// Closing delimiter at end-of-file without a trailing newline.
	if meta, ok = bytes.CutSuffix(rest, []byte("\n---")); ok {
		return meta, nil
	}
	// No closing delimiter: not frontmatter after all.
	return nil, normalized
}
