package release

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validManifest() *Manifest {
	return &Manifest{
		SchemaVersion: SchemaVersion,
		Component:     "order-extractor",
		Version:       "1.4.2",
		CreatedAt:     time.Date(2026, 7, 26, 9, 14, 22, 0, time.UTC),
		CreatedBy:     "robin.hultman@integrio.se",
		Source:        Source{Commit: strings.Repeat("a", 40), Ref: "HEAD"},
		Images:        []Image{{Name: "harbor.intropy.io/order-extractor", Digest: "sha256:" + strings.Repeat("b", 64)}},
		Notes:         "- Handle empty payloads\n",
		Changes:       []Change{{Commit: strings.Repeat("c", 40), Subject: "Handle empty payloads"}},
		ChangeBasis:   ChangeBasis{Kind: BasisRelease, Version: "1.4.1", Commit: strings.Repeat("d", 40)},
	}
}

func TestValidateAcceptsAWellFormedManifest(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejections(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Manifest)
		want string
	}{
		{"wrong schema version", func(m *Manifest) { m.SchemaVersion = 2 }, "schemaVersion"},
		{"no component", func(m *Manifest) { m.Component = "" }, "component"},
		{"no version", func(m *Manifest) { m.Version = "" }, "version"},
		{"no commit", func(m *Manifest) { m.Source.Commit = "" }, "source.commit"},
		{"no images", func(m *Manifest) { m.Images = nil }, "no images"},
		{"image without name", func(m *Manifest) { m.Images[0].Name = "" }, "images[0].name"},
		{"image with a tag not a digest", func(m *Manifest) { m.Images[0].Digest = "1.4.2" }, "not a sha256 digest"},
		{"basis missing", func(m *Manifest) { m.ChangeBasis = ChangeBasis{} }, "changeBasis.kind is empty"},
		{"basis unknown", func(m *Manifest) { m.ChangeBasis = ChangeBasis{Kind: "guessed"} }, "unknown"},
		{"release basis without a commit", func(m *Manifest) {
			m.ChangeBasis = ChangeBasis{Kind: BasisRelease, Version: "1.4.1"}
		}, "commit is empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mut(m)

			err := m.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %s", tc.name)
			}
			if !errors.Is(err, ErrInvalidManifest) {
				t.Errorf("error %v should wrap ErrInvalidManifest", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

// An initial release has no changes, and that must validate: it is the honest
// state, not a malformed one.
func TestValidateAcceptsAnInitialReleaseWithNoChanges(t *testing.T) {
	m := validManifest()
	m.Changes = nil
	m.ChangeBasis = ChangeBasis{Kind: BasisInitial}
	m.Notes = InitialNotes

	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A re-run publishes a manifest with a later createdAt, so those two fields
// must not count towards identity or every retry would look like a conflict.
func TestSameReleaseIgnoresCreationMetadata(t *testing.T) {
	a := validManifest()
	b := validManifest()
	b.CreatedAt = a.CreatedAt.Add(time.Hour)
	b.CreatedBy = "someone.else@integrio.se"

	if !a.SameRelease(b) {
		t.Error("manifests differing only in createdAt/createdBy should be the same release")
	}
}

func TestSameReleaseDetectsRealDifferences(t *testing.T) {
	cases := map[string]func(*Manifest){
		"commit":  func(m *Manifest) { m.Source.Commit = strings.Repeat("e", 40) },
		"digest":  func(m *Manifest) { m.Images[0].Digest = "sha256:" + strings.Repeat("f", 64) },
		"image":   func(m *Manifest) { m.Images = append(m.Images, Image{Name: "x", Digest: "sha256:x"}) },
		"notes":   func(m *Manifest) { m.Notes = "- Something else\n" },
		"basis":   func(m *Manifest) { m.ChangeBasis = ChangeBasis{Kind: BasisInitial} },
		"changes": func(m *Manifest) { m.Changes = nil },
		"version": func(m *Manifest) { m.Version = "1.4.3" },
	}

	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			a := validManifest()
			b := validManifest()
			mut(b)

			if a.SameRelease(b) {
				t.Errorf("manifests differing in %s should not be the same release", name)
			}
		})
	}
}

// The empty-changes ambiguity is the whole reason changeBasis exists, so the
// field must survive a JSON round trip.
func TestChangeBasisDistinguishesEmptyChanges(t *testing.T) {
	initial := validManifest()
	initial.Changes = nil
	initial.ChangeBasis = ChangeBasis{Kind: BasisInitial}

	unchanged := validManifest()
	unchanged.Changes = nil

	initialJSON, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	unchangedJSON, err := json.Marshal(unchanged)
	if err != nil {
		t.Fatal(err)
	}

	// Both serialise "changes": null/[]; only the basis tells them apart.
	if string(initialJSON) == string(unchangedJSON) {
		t.Fatal("an initial release and an unchanged release must not serialise identically")
	}

	var back Manifest
	if err := json.Unmarshal(initialJSON, &back); err != nil {
		t.Fatal(err)
	}
	if back.ChangeBasis.Kind != BasisInitial {
		t.Errorf("changeBasis.kind = %q after round trip, want %q", back.ChangeBasis.Kind, BasisInitial)
	}
}
