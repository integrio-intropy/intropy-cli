package release

import (
	"strings"
	"testing"
)

func TestComposeNotes(t *testing.T) {
	changelog := "- Handle empty payloads\n- Retry on 429\n"
	for _, tc := range []struct {
		name, provided, generated, want string
	}{
		{"generated alone", "", changelog, changelog},
		{"provided above the changelog", "Faster retries.", changelog, "Faster retries.\n\n" + changelog},
		{"provided replaces the initial placeholder", "First managed release.", InitialNotes, "First managed release.\n"},
		{"provided with nothing generated", "Rebuild only.", "", "Rebuild only.\n"},
		{"whitespace-only counts as none", " \n\t\n", changelog, changelog},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComposeNotes(tc.provided, tc.generated); got != tc.want {
				t.Errorf("ComposeNotes() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A retry must reproduce the notes exactly, so a file that only differs in
// line endings or a trailing newline has to normalise to the same text.
func TestNormalizeNotesIsStableAcrossEditors(t *testing.T) {
	unix := NormalizeNotes("# 1.4.2\n\nFaster retries.\n")
	for _, variant := range []string{
		"# 1.4.2\r\n\r\nFaster retries.\r\n",
		"# 1.4.2\n\nFaster retries.",
		"\n# 1.4.2\n\nFaster retries.\n\n\n",
	} {
		if got := NormalizeNotes(variant); got != unix {
			t.Errorf("NormalizeNotes(%q) = %q, want %q", variant, got, unix)
		}
	}
}

func TestFirstLineIsTheListingSummary(t *testing.T) {
	for in, want := range map[string]string{
		"- Handle empty payloads\n- Retry\n": "Handle empty payloads",
		"# Faster retries\n\nBody":           "Faster retries",
		"## Faster retries":                  "Faster retries",
		"#123 fix the thing":                 "#123 fix the thing",
		"Plain summary\nmore":                "Plain summary",
	} {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnlyNotesDiffer(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{
			SchemaVersion: SchemaVersion,
			Component:     "order-extractor",
			Version:       "1.0.0",
			Source:        Source{Commit: strings.Repeat("a", 40), Ref: "HEAD"},
			Images:        []Image{{Name: "r/order-extractor", Digest: "sha256:abc"}},
			Notes:         "one\n",
			ChangeBasis:   ChangeBasis{Kind: BasisInitial},
		}
	}

	notes := base()
	notes.Notes = "two\n"
	if !base().onlyNotesDiffer(notes) {
		t.Error("manifests differing only in notes were not recognised")
	}

	commit := base()
	commit.Notes = "two\n"
	commit.Source.Commit = strings.Repeat("b", 40)
	if base().onlyNotesDiffer(commit) {
		t.Error("a different commit is a different release, not a notes mismatch")
	}

	if base().onlyNotesDiffer(base()) {
		t.Error("identical manifests do not differ in notes")
	}
}
