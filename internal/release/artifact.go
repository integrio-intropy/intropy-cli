package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// Media types for the release artifact. These are as immutable as the schema:
// a published manifest is identified by them forever.
const (
	ArtifactType  = "application/vnd.integrio.release.v1"
	MediaTypeJSON = "application/vnd.integrio.release.v1+json"
)

// Annotations mirrored onto the manifest so a registry UI shows something
// useful without pulling the layer. release list reads them for the same
// reason: a listing costs one manifest fetch per release instead of a blob pull.
const (
	AnnotationComponent = "io.intropy.release.component"
	AnnotationCommit    = "io.intropy.release.source-commit"

	annotationTitle       = "org.opencontainers.image.title"
	annotationVersion     = "org.opencontainers.image.version"
	annotationCreated     = "org.opencontainers.image.created"
	annotationRevision    = "org.opencontainers.image.revision"
	annotationDescription = "org.opencontainers.image.description"
)

// createdLayout is how annotationCreated is written, and so how it must be read.
const createdLayout = "2006-01-02T15:04:05Z"

// ReleasesRepoSuffix is appended to an image repository to locate that
// component's releases. Releases live beside the images they describe so a
// component needs no registry configuration beyond the one it already has.
const ReleasesRepoSuffix = "/releases"

var (
	ErrNotFound     = registry.ErrNotFound
	ErrUnauthorized = registry.ErrUnauthorized

	// ErrNotRelease means the tag exists but holds something else. Overwriting
	// it would destroy whatever that is, so it is never treated as absent.
	ErrNotRelease = errors.New("artifact is not a release manifest")
)

// ReleasesRepo returns the repository holding a component's releases, given
// any one of its image repositories.
func ReleasesRepo(image string) string {
	return strings.TrimSuffix(image, "/") + ReleasesRepoSuffix
}

// Ref builds the reference for one version of a component's releases.
func Ref(releasesRepo, version string) string {
	return releasesRepo + ":" + version
}

// Registry is the registry surface a release needs. It is an interface so the
// commands can be tested against an in-memory registry, and so callers never
// import internal/registry directly.
type Registry interface {
	PushArtifact(ctx context.Context, ref string, art registry.Artifact) (registry.Descriptor, error)
	PullArtifact(ctx context.Context, ref string) (registry.Artifact, registry.Descriptor, error)
	Resolve(ctx context.Context, ref string) (registry.Descriptor, error)
	ListTags(ctx context.Context, ref string) ([]string, error)
}

// NewRegistry builds the production registry client. Replaced in tests,
// following the newSkillRegistry pattern in cmd/intropy.
var NewRegistry = func(userAgent string) (Registry, error) {
	return registry.NewClient(registry.WithUserAgent(userAgent))
}

// Push publishes a manifest at ref and returns its digest.
func Push(ctx context.Context, reg Registry, ref string, m *Manifest) (string, error) {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode release manifest: %w", err)
	}
	body = append(body, '\n')

	desc, err := reg.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: ArtifactType,
		Layers:       []registry.Blob{{MediaType: MediaTypeJSON, Data: body}},
		Annotations: map[string]string{
			AnnotationComponent:   m.Component,
			AnnotationCommit:      m.Source.Commit,
			annotationTitle:       m.Component,
			annotationVersion:     m.Version,
			annotationCreated:     m.CreatedAt.UTC().Format(createdLayout),
			annotationRevision:    m.Source.Commit,
			annotationDescription: firstLine(m.Notes),
		},
	})
	if err != nil {
		return "", fmt.Errorf("push release %s: %w", ref, err)
	}
	return desc.Digest, nil
}

// Pull reads and validates the manifest at ref.
func Pull(ctx context.Context, reg Registry, ref string) (*Manifest, error) {
	art, _, err := reg.PullArtifact(ctx, ref)
	if err != nil {
		return nil, err
	}
	if art.ArtifactType != ArtifactType {
		return nil, fmt.Errorf("%w: %s has artifact type %q, want %q", ErrNotRelease, ref, art.ArtifactType, ArtifactType)
	}
	if len(art.Layers) != 1 {
		return nil, fmt.Errorf("%w: %s has %d layers, want exactly 1", ErrNotRelease, ref, len(art.Layers))
	}
	if art.Layers[0].MediaType != MediaTypeJSON {
		return nil, fmt.Errorf("%w: %s layer has media type %q, want %q", ErrNotRelease, ref, art.Layers[0].MediaType, MediaTypeJSON)
	}

	var m Manifest
	if err := json.Unmarshal(art.Layers[0].Data, &m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNotRelease, ref, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return &m, nil
}

// ListVersions returns the versions released for a component, in whatever
// order the registry reports them.
//
// A releases repository that does not exist yet reports ErrNotFound, which
// callers read as "never released" rather than as a failure.
func ListVersions(ctx context.Context, reg Registry, releasesRepo string) ([]string, error) {
	return reg.ListTags(ctx, releasesRepo)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimPrefix(line, "- ")
}
