package deploy

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"
)

// PlaceholderPrefix marks a value only a human can supply — a connection string,
// a host, a credential, a cron schedule.
//
// The spelling is load-bearing. It contains no YAML metacharacter, so it is a
// legal unquoted plain scalar in every position including the start of a value;
// it is legal inside a URL; the hint after the prefix says what to supply, so a
// grep hit is self-explanatory; and one regexp finds every instance.
const PlaceholderPrefix = "REPLACE-ME-"

// A hint of at least one character, ending in an alphanumeric so trailing
// punctuation in prose ("set REPLACE-ME-SFTP-HOST, then …") is not swallowed.
var placeholderPattern = regexp.MustCompile(`REPLACE-ME-[A-Z0-9-]*[A-Z0-9]`)

// Placeholder is one unfilled value in one line of one file.
type Placeholder struct {
	// File is relative to the scanned root, slash-separated.
	File string `json:"file"`
	Line int    `json:"line"`

	// Token is the whole marker including its hint, so a reader knows what the
	// value is without opening the file.
	Token string `json:"token"`
}

// scanPlaceholders reports every placeholder in rels, which are slash-separated
// paths relative to root.
//
// A path that does not exist is skipped rather than an error: the same function
// serves --plan, where it scans a staging directory, and a re-run, where it scans
// the committed tree and some of rels were never written.
//
// An unpinned image tag is deliberately *not* a placeholder. Pinning is
// intropy deploy's job, and inviting someone to hand-edit an image tag would
// undo the digest discipline the rest of this package exists to enforce.
func scanPlaceholders(root string, rels []string) ([]Placeholder, error) {
	var out []Placeholder
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if errorsIsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan %s for placeholders: %w", rel, err)
		}
		// A binary file cannot carry a placeholder a human would edit, and
		// reporting a line number in one would be meaningless.
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, token := range placeholderPattern.FindAllString(line, -1) {
				out = append(out, Placeholder{File: rel, Line: i + 1, Token: token})
			}
		}
	}

	slices.SortFunc(out, func(a, b Placeholder) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Token, b.Token)
	})
	return out, nil
}

func errorsIsNotExist(err error) bool {
	return os.IsNotExist(err) || err == fs.ErrNotExist
}

// reportPlaceholders writes the placeholder table and the summary line.
//
// Remaining placeholders are the expected outcome, never a failure: complete
// manifests are explicitly not the goal, and the command's value is that the
// remaining work is now a bounded, greppable list.
func reportPlaceholders(out output, found []Placeholder) {
	if len(found) == 0 {
		fmt.Fprintln(out.Stdout, "\nno placeholders left to fill in")
		return
	}

	fmt.Fprintf(out.Stdout, "\n%s\n", summarisePlaceholders(found))
	tw := tabwriter.NewWriter(out.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range found {
		fmt.Fprintf(tw, "  %s:%d\t%s\n", p.File, p.Line, p.Token)
	}
	_ = tw.Flush()
	fmt.Fprintf(out.Stdout, "\nimage tags are not placeholders: 'intropy deploy' pins digests, so leave them alone\n")
}

func summarisePlaceholders(found []Placeholder) string {
	files := map[string]bool{}
	for _, p := range found {
		files[p.File] = true
	}
	return fmt.Sprintf("%d placeholder%s to fill in across %d file%s:",
		len(found), plural(len(found)), len(files), plural(len(files)))
}
