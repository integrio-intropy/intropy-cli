package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// PushIndex publishes a Skills Collection (an OCI Image Index per §5.1) to a
// registry. The index references skill manifests by digest; the skills
// themselves must already be published.
func (c *Client) PushIndex(ctx context.Context, ref string, index Index) (Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse ref: %w", err)
	}
	if parsed.Tag == "" {
		return Descriptor{}, fmt.Errorf("collection ref must include a tag")
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Descriptor{}, err
	}

	// Spec-compliant registries (Harbor et al.) reject an Image Index whose
	// referenced manifests aren't present in the target repository. The OCI
	// distribution spec allows cross-repo mount only for blobs, not manifests.
	// Copy each child manifest from its source repo into the collection repo
	// first; oras-go uses cross-repo blob mount automatically when src and dst
	// share a registry, so only the manifest JSON is re-uploaded per skill.
	for _, entry := range index.Manifests {
		srcRef, err := ParseReference(entry.Ref)
		if err != nil {
			return Descriptor{}, fmt.Errorf("parse skill ref %q: %w", entry.Ref, err)
		}
		src, err := c.repository(srcRef)
		if err != nil {
			return Descriptor{}, fmt.Errorf("source repo for %q: %w", entry.Ref, err)
		}
		childDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    digest.Digest(entry.Digest),
			Size:      entry.Size,
		}
		if err := oras.CopyGraph(ctx, src, repo, childDesc, oras.DefaultCopyGraphOptions); err != nil {
			return Descriptor{}, fmt.Errorf("copy %s into %s: %w", entry.Ref, parsed.Repository, mapError(err, entry.Ref))
		}
	}

	manifests := make([]ocispec.Descriptor, 0, len(index.Manifests))
	for _, entry := range index.Manifests {
		annotations := map[string]string{
			AnnotationSkillName:                    entry.Name,
			AnnotationSkillRef:                     entry.Ref,
			"org.opencontainers.image.title":       entry.Name,
			"org.opencontainers.image.version":     entry.Version,
			"org.opencontainers.image.description": entry.Description,
		}
		manifests = append(manifests, ocispec.Descriptor{
			MediaType:    ocispec.MediaTypeImageManifest,
			ArtifactType: MediaTypeSkillArtifact,
			Digest:       digest.Digest(entry.Digest),
			Size:         entry.Size,
			Annotations:  annotations,
		})
	}

	ociIndex := ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: MediaTypeCollection,
		Manifests:    manifests,
		Annotations:  index.Annotations,
	}

	indexBytes, err := json.Marshal(ociIndex)
	if err != nil {
		return Descriptor{}, fmt.Errorf("marshal index: %w", err)
	}

	indexDesc := ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: MediaTypeCollection,
		Digest:       digest.FromBytes(indexBytes),
		Size:         int64(len(indexBytes)),
	}

	if err := repo.Manifests().PushReference(ctx, indexDesc, bytes.NewReader(indexBytes), parsed.Tag); err != nil {
		return Descriptor{}, mapError(err, ref)
	}

	return Descriptor{
		MediaType:    indexDesc.MediaType,
		ArtifactType: MediaTypeCollection,
		Digest:       indexDesc.Digest.String(),
		Size:         indexDesc.Size,
		Annotations:  ociIndex.Annotations,
	}, nil
}
