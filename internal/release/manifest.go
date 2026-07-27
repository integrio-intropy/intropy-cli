// Package release publishes and reads immutable release manifests.
//
// A release names a set of built bits: a component version, the source commit
// it was built from, the image digests CI published for that commit, and notes
// describing what changed. It is metadata only — creating a release changes no
// environment. Deployments pin digests; releases record which digests belong
// together under a version.
//
// The manifest is stored as a tag-addressed OCI artifact alongside the images
// it describes, so nothing but the OCI Distribution Spec is needed to read it.
package release

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// SchemaVersion is the manifest format version. Bump only for incompatible
// changes; additive fields do not require a bump.
const SchemaVersion = 1

// Manifest is the immutable record of one release.
//
// Field names are stable and additive-only. These documents outlive the CLI
// that wrote them — they sit in customer registries, and the format is
// immutable by design — so a rename is a breaking change to every reader.
type Manifest struct {
	SchemaVersion int         `json:"schemaVersion"`
	Component     string      `json:"component"`
	Version       string      `json:"version"`
	CreatedAt     time.Time   `json:"createdAt"`
	CreatedBy     string      `json:"createdBy"`
	Source        Source      `json:"source"`
	Images        []Image     `json:"images"`
	Notes         string      `json:"notes"`
	Changes       []Change    `json:"changes"`
	ChangeBasis   ChangeBasis `json:"changeBasis"`
}

// Source identifies the commit the release was built from.
type Source struct {
	Commit string `json:"commit"`
	Ref    string `json:"ref"`
	Repo   string `json:"repo,omitempty"`
}

// Image is one image repository pinned to the digest CI published.
//
// There is deliberately no build timestamp: nothing available when a release
// is created knows when the image was built, and a field that is sometimes a
// guess is worse than no field.
type Image struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// Change is one commit included in the release.
type Change struct {
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
}

// Basis kinds. A reader that meets an unknown kind should treat Changes as
// present-but-unexplained rather than empty.
const (
	// BasisInitial means nothing had been released, so there was no prior
	// state to compare against.
	BasisInitial = "initial"

	// BasisRelease means Changes was computed against an earlier release.
	BasisRelease = "release"

	// BasisExplicit means the operator named the starting point.
	BasisExplicit = "explicit"
)

// ChangeBasis records what Changes was computed against.
//
// Without it an empty Changes is ambiguous: "nothing changed since the last
// release" and "there was no basis for comparison" are very different claims
// and read identically as []. Every manifest states which one it means.
type ChangeBasis struct {
	Kind string `json:"kind"`

	// Version and Commit identify the predecessor when Kind is BasisRelease.
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`

	// Ref is what the operator passed to --since when Kind is BasisExplicit.
	Ref string `json:"ref,omitempty"`
}

// Describe renders the basis for a person reading the release.
func (b ChangeBasis) Describe() string {
	switch b.Kind {
	case BasisInitial:
		return "initial release (no previous release to compare against)"
	case BasisRelease:
		return fmt.Sprintf("changes since %s (%s)", b.Version, shortSHA(b.Commit))
	case BasisExplicit:
		return fmt.Sprintf("changes since %s (given with --since)", b.Ref)
	default:
		return b.Kind
	}
}

// ErrInvalidManifest is the base for every validation failure, so callers can
// tell a malformed release from a transport error.
var ErrInvalidManifest = errors.New("invalid release manifest")

// Validate checks a manifest read back from a registry. It is deliberately
// strict about the fields that make a release meaningful and silent about the
// rest: a release with no notes is fine, a release with no digest is not.
func (m *Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schemaVersion %d is not supported (this CLI writes and reads %d)", ErrInvalidManifest, m.SchemaVersion, SchemaVersion)
	}
	if m.Component == "" {
		return fmt.Errorf("%w: component is empty", ErrInvalidManifest)
	}
	if m.Version == "" {
		return fmt.Errorf("%w: version is empty", ErrInvalidManifest)
	}
	if m.Source.Commit == "" {
		return fmt.Errorf("%w: source.commit is empty", ErrInvalidManifest)
	}
	if len(m.Images) == 0 {
		return fmt.Errorf("%w: no images", ErrInvalidManifest)
	}
	for i, img := range m.Images {
		if img.Name == "" {
			return fmt.Errorf("%w: images[%d].name is empty", ErrInvalidManifest, i)
		}
		if !strings.HasPrefix(img.Digest, "sha256:") {
			return fmt.Errorf("%w: images[%d].digest %q is not a sha256 digest", ErrInvalidManifest, i, img.Digest)
		}
	}
	switch m.ChangeBasis.Kind {
	case BasisInitial, BasisExplicit:
	case BasisRelease:
		if m.ChangeBasis.Commit == "" {
			return fmt.Errorf("%w: changeBasis.kind is %q but commit is empty", ErrInvalidManifest, BasisRelease)
		}
	case "":
		return fmt.Errorf("%w: changeBasis.kind is empty, so changes cannot be interpreted", ErrInvalidManifest)
	default:
		return fmt.Errorf("%w: changeBasis.kind %q is unknown", ErrInvalidManifest, m.ChangeBasis.Kind)
	}
	return nil
}

// SameRelease reports whether two manifests describe the same release.
//
// CreatedAt and CreatedBy are excluded: re-running release create for a
// version that already exists must recognise its own earlier work, and those
// two fields differ on every run by construction. Everything else is what the
// release actually claims, and any difference there means the version is being
// reused for something else.
func (m *Manifest) SameRelease(other *Manifest) bool {
	if m == nil || other == nil {
		return false
	}
	if m.SchemaVersion != other.SchemaVersion ||
		m.Component != other.Component ||
		m.Version != other.Version ||
		m.Source != other.Source ||
		m.Notes != other.Notes ||
		m.ChangeBasis != other.ChangeBasis {
		return false
	}
	if len(m.Images) != len(other.Images) {
		return false
	}
	for i := range m.Images {
		if m.Images[i] != other.Images[i] {
			return false
		}
	}
	if len(m.Changes) != len(other.Changes) {
		return false
	}
	for i := range m.Changes {
		if m.Changes[i] != other.Changes[i] {
			return false
		}
	}
	return true
}

func shortSHA(sha string) string {
	if len(sha) >= 40 {
		return sha[:7]
	}
	return sha
}
