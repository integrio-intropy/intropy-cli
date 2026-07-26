package deploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// CommitTagPrefix is prepended to a commit sha to form the image tag CI
// publishes. Kept in one place: if the pipeline's tagging scheme ever differs,
// this is the only line that changes.
const CommitTagPrefix = "sha-"

// CommitTag returns the registry tag for a commit.
func CommitTag(commit string) string { return CommitTagPrefix + commit }

// Pin is one image repository resolved to an immutable digest.
type Pin struct {
	// Image is the bare repository, as declared in component.yaml.
	Image string

	// Digest is the manifest digest, in sha256:… form.
	Digest string

	// Tag is the tag the digest was resolved from, kept for messages.
	Tag string
}

// Ref renders the pinned reference in the form kustomize writes.
func (p Pin) Ref() string { return p.Image + "@" + p.Digest }

// Resolver resolves an image reference to a descriptor. It is the seam that
// lets digest resolution be tested against an in-memory registry.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (registry.Descriptor, error)
}

// NewResolver builds the production resolver. Replaced in tests, following the
// newSkillRegistry pattern in cmd/intropy.
var NewResolver = func(userAgent string) (Resolver, error) {
	return registry.NewClient(registry.WithUserAgent(userAgent))
}

// ResolveDigests resolves every image in the component to the digest CI
// published for commit.
//
// The digest comes from whatever the registry actually returns for the tag,
// which is the point of resolving rather than constructing a reference: for a
// multi-architecture build the tag points at an image index, and the index
// digest is what must be pinned. Registries have also been known to convert
// between Docker and OCI manifest types on read, changing the digest, so the
// only safe value is the one this lookup observed.
func ResolveDigests(ctx context.Context, r Resolver, comp *ComponentConfig, commit string) ([]Pin, error) {
	tag := CommitTag(commit)

	pins := make([]Pin, 0, len(comp.Images))
	for _, img := range comp.Images {
		ref := img.Name + ":" + tag
		desc, err := r.Resolve(ctx, ref)
		if err != nil {
			return nil, resolveError(err, img.Name, tag)
		}
		if desc.Digest == "" {
			return nil, fmt.Errorf("registry returned no digest for %s", ref)
		}
		pins = append(pins, Pin{Image: img.Name, Digest: desc.Digest, Tag: tag})
	}
	return pins, nil
}

// resolveError turns a registry failure into something a caller can act on.
// A missing tag is the single most common case by far — it means the pipeline
// has not finished publishing — and deserves to say so rather than read as a
// generic 404.
func resolveError(err error, image, tag string) error {
	if errors.Is(err, registry.ErrNotFound) {
		return fmt.Errorf("%s has no %s tag yet: the pipeline has not published an image for this commit (or was not triggered for it)", image, tag)
	}
	return fmt.Errorf("resolve %s:%s: %w", image, tag, err)
}
