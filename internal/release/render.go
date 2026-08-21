package release

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// renderCreated reports what Create did.
func renderCreated(w io.Writer, r Result) error {
	verb := "published"
	if !r.Created {
		verb = "already published"
	}
	fmt.Fprintf(w, "%s %s %s\n", verb, r.Component, r.Version)
	fmt.Fprintf(w, "  %s\n", r.Ref)
	if r.Digest != "" {
		fmt.Fprintf(w, "  %s\n", r.Digest)
	}
	if r.Tagged {
		fmt.Fprintf(w, "  tag %s\n", r.Tag)
	}
	fmt.Fprintln(w)
	return renderManifest(w, r.Manifest)
}

// renderManifest is the human view of a release.
func renderManifest(w io.Writer, m *Manifest) error {
	if m == nil {
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "component\t%s\n", m.Component)
	fmt.Fprintf(tw, "version\t%s\n", m.Version)
	fmt.Fprintf(tw, "created\t%s\n", m.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	if m.CreatedBy != "" {
		fmt.Fprintf(tw, "created by\t%s\n", m.CreatedBy)
	}
	fmt.Fprintf(tw, "commit\t%s\n", m.Source.Commit)
	for i, img := range m.Images {
		label := "images"
		if i > 0 {
			label = ""
		}
		fmt.Fprintf(tw, "%s\t%s@%s\n", label, img.Name, img.Digest)
	}
	fmt.Fprintf(tw, "basis\t%s\n", m.ChangeBasis.Describe())
	if err := tw.Flush(); err != nil {
		return err
	}

	notes := strings.TrimRight(m.Notes, "\n")
	if notes == "" {
		return nil
	}
	fmt.Fprintf(w, "\nnotes:\n%s\n", indent(notes, "  "))
	return nil
}

// noValue is what an empty cell renders as, matching deploy status.
const noValue = "—"

// renderReleases is the human view of a component's releases.
func renderReleases(w io.Writer, releases []Summary) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tCREATED\tCOMMIT\tNOTES")
	for _, r := range releases {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Version, createdCell(r.CreatedAt), orNone(shortSHA(r.Commit)), orNone(r.Notes))
	}
	return tw.Flush()
}

// createdCell renders the release date at day precision: the interesting
// question in a list is which release is newer, not what time of day it was cut.
func createdCell(t *time.Time) string {
	if t == nil {
		return noValue
	}
	return t.UTC().Format("2006-01-02")
}

func orNone(s string) string {
	if s == "" {
		return noValue
	}
	return s
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
