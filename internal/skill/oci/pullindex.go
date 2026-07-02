package oci

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

// PullIndex fetches a Skills Collection (an OCI Image Index per §5.1) and
// returns its parsed contents. The skill artifacts the index references are
// not pulled — only the index itself.
func (c *Client) PullIndex(ctx context.Context, ref string) (Index, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Index{}, fmt.Errorf("parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Index{}, err
	}

	target := parsed.Tag
	if target == "" {
		target = parsed.Digest
	}

	store := memory.New()
	indexDesc, err := oras.Copy(ctx, repo, target, store, target, oras.DefaultCopyOptions)
	if err != nil {
		return Index{}, mapError(err, ref)
	}

	indexBytes, err := content.FetchAll(ctx, store, indexDesc)
	if err != nil {
		return Index{}, fmt.Errorf("fetch index: %w", err)
	}

	var ociIndex ocispec.Index
	if err := json.Unmarshal(indexBytes, &ociIndex); err != nil {
		return Index{}, fmt.Errorf("parse index: %w", err)
	}

	if ociIndex.ArtifactType != MediaTypeCollection {
		return Index{}, fmt.Errorf("not a skills collection: artifactType is %q", ociIndex.ArtifactType)
	}

	entries := make([]IndexEntry, 0, len(ociIndex.Manifests))
	for _, m := range ociIndex.Manifests {
		entries = append(entries, IndexEntry{
			Name:        m.Annotations[AnnotationSkillName],
			Ref:         m.Annotations[AnnotationSkillRef],
			Version:     m.Annotations["org.opencontainers.image.version"],
			Description: m.Annotations["org.opencontainers.image.description"],
			Digest:      m.Digest.String(),
		})
	}

	return Index{
		Annotations: ociIndex.Annotations,
		Manifests:   entries,
	}, nil
}
