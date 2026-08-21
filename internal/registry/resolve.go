package registry

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

// Resolve returns the descriptor for the manifest at ref, including its
// annotations. ref may carry a tag or a digest.
func (c *Client) Resolve(ctx context.Context, ref string) (Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Descriptor{}, err
	}

	desc, err := repo.Resolve(ctx, parsed.TagOrDigest())
	if err != nil {
		return Descriptor{}, mapError(err, parsed)
	}

	manifestBytes, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return Descriptor{}, fmt.Errorf("fetch manifest %s: %w", ref, mapError(err, parsed))
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Descriptor{}, fmt.Errorf("parse manifest %s: %w", ref, err)
	}

	return Descriptor{
		MediaType: desc.MediaType,
		// The manifest body is authoritative for its own artifact type. A
		// descriptor built from a tag resolution carries only what the
		// registry's headers said, which is media type, digest and size — so
		// without this the field would silently always be empty here.
		ArtifactType: cmp.Or(manifest.ArtifactType, desc.ArtifactType),
		Digest:       desc.Digest.String(),
		Size:         desc.Size,
		Annotations:  manifest.Annotations,
	}, nil
}
