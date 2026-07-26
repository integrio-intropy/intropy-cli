package oci

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// PushIndex publishes a Skills Collection (an OCI Image Index per §5.1) to a
// registry. The index references skill manifests by digest; the skills
// themselves must already be published. Each referenced manifest is copied
// into the collection repository first — see registry.Client.PushIndex.
func (c *Client) PushIndex(ctx context.Context, ref string, index Index) (Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse ref: %w", err)
	}
	if parsed.Tag == "" {
		return Descriptor{}, fmt.Errorf("collection ref must include a tag")
	}

	manifests := make([]registry.IndexManifest, 0, len(index.Manifests))
	for _, entry := range index.Manifests {
		manifests = append(manifests, registry.IndexManifest{
			Descriptor: registry.Descriptor{
				MediaType:    ocispec.MediaTypeImageManifest,
				ArtifactType: MediaTypeSkillArtifact,
				Digest:       entry.Digest,
				Size:         entry.Size,
				Annotations: map[string]string{
					AnnotationSkillName:                    entry.Name,
					AnnotationSkillRef:                     entry.Ref,
					"org.opencontainers.image.title":       entry.Name,
					"org.opencontainers.image.version":     entry.Version,
					"org.opencontainers.image.description": entry.Description,
				},
			},
			SourceRef: entry.Ref,
		})
	}

	return c.reg.PushIndex(ctx, ref, registry.Index{
		ArtifactType: MediaTypeCollection,
		Annotations:  index.Annotations,
		Manifests:    manifests,
	})
}
