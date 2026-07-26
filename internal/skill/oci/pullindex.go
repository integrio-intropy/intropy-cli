package oci

import (
	"context"
	"fmt"
)

// PullIndex fetches a Skills Collection (an OCI Image Index per §5.1) and
// returns its parsed contents. The skill artifacts the index references are
// not pulled — only the index itself.
func (c *Client) PullIndex(ctx context.Context, ref string) (Index, error) {
	index, _, err := c.reg.PullIndex(ctx, ref)
	if err != nil {
		return Index{}, err
	}

	if index.ArtifactType != MediaTypeCollection {
		return Index{}, fmt.Errorf("not a skills collection: artifactType is %q", index.ArtifactType)
	}

	entries := make([]IndexEntry, 0, len(index.Manifests))
	for _, m := range index.Manifests {
		entries = append(entries, IndexEntry{
			Name:        m.Descriptor.Annotations[AnnotationSkillName],
			Ref:         m.Descriptor.Annotations[AnnotationSkillRef],
			Version:     m.Descriptor.Annotations["org.opencontainers.image.version"],
			Description: m.Descriptor.Annotations["org.opencontainers.image.description"],
			Digest:      m.Descriptor.Digest,
		})
	}

	return Index{
		Annotations: index.Annotations,
		Manifests:   entries,
	}, nil
}
