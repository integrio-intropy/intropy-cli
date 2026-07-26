package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

// PushIndex publishes an OCI Image Index to the registry at ref. The ref
// must include a tag.
//
// Spec-compliant registries (Harbor et al.) reject an Image Index whose
// referenced manifests aren't present in the target repository, and the OCI
// distribution spec allows cross-repo mount only for blobs, not manifests.
// For every entry with a SourceRef, the child manifest is therefore copied
// from its source repository into the target repository first.
func (c *Client) PushIndex(ctx context.Context, ref string, index Index) (Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse ref: %w", err)
	}
	if parsed.Tag == "" {
		return Descriptor{}, fmt.Errorf("ref must include a tag")
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Descriptor{}, err
	}

	for _, entry := range index.Manifests {
		if entry.SourceRef == "" {
			continue
		}
		srcRef, err := ParseReference(entry.SourceRef)
		if err != nil {
			return Descriptor{}, fmt.Errorf("parse manifest ref %q: %w", entry.SourceRef, err)
		}
		src, err := c.repository(srcRef)
		if err != nil {
			return Descriptor{}, fmt.Errorf("source repo for %q: %w", entry.SourceRef, err)
		}
		childDesc := ocispec.Descriptor{
			MediaType: entry.Descriptor.MediaType,
			Digest:    digest.Digest(entry.Descriptor.Digest),
			Size:      entry.Descriptor.Size,
		}
		if err := oras.CopyGraph(ctx, src, repo, childDesc, oras.DefaultCopyGraphOptions); err != nil {
			return Descriptor{}, fmt.Errorf("copy %s into %s: %w", entry.SourceRef, parsed.Repository, mapError(err, srcRef))
		}
	}

	manifests := make([]ocispec.Descriptor, 0, len(index.Manifests))
	for _, entry := range index.Manifests {
		manifests = append(manifests, ocispec.Descriptor{
			MediaType:    entry.Descriptor.MediaType,
			ArtifactType: entry.Descriptor.ArtifactType,
			Digest:       digest.Digest(entry.Descriptor.Digest),
			Size:         entry.Descriptor.Size,
			Annotations:  entry.Descriptor.Annotations,
		})
	}

	ociIndex := ocispec.Index{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: index.ArtifactType,
		Manifests:    manifests,
		Annotations:  index.Annotations,
	}

	indexBytes, err := json.Marshal(ociIndex)
	if err != nil {
		return Descriptor{}, fmt.Errorf("marshal index: %w", err)
	}

	indexDesc := ocispec.Descriptor{
		MediaType:    ocispec.MediaTypeImageIndex,
		ArtifactType: index.ArtifactType,
		Digest:       digest.FromBytes(indexBytes),
		Size:         int64(len(indexBytes)),
	}

	if err := repo.Manifests().PushReference(ctx, indexDesc, bytes.NewReader(indexBytes), parsed.Tag); err != nil {
		return Descriptor{}, mapError(err, parsed)
	}

	return Descriptor{
		MediaType:    indexDesc.MediaType,
		ArtifactType: index.ArtifactType,
		Digest:       indexDesc.Digest.String(),
		Size:         indexDesc.Size,
		Annotations:  ociIndex.Annotations,
	}, nil
}

// PullIndex fetches an OCI Image Index and returns its parsed contents. The
// manifests the index references are not pulled — only the index itself.
func (c *Client) PullIndex(ctx context.Context, ref string) (Index, Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Index{}, Descriptor{}, fmt.Errorf("parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Index{}, Descriptor{}, err
	}

	target := parsed.TagOrDigest()

	store := memory.New()
	indexDesc, err := oras.Copy(ctx, repo, target, store, target, oras.DefaultCopyOptions)
	if err != nil {
		return Index{}, Descriptor{}, mapError(err, parsed)
	}

	indexBytes, err := content.FetchAll(ctx, store, indexDesc)
	if err != nil {
		return Index{}, Descriptor{}, fmt.Errorf("fetch index: %w", err)
	}

	var ociIndex ocispec.Index
	if err := json.Unmarshal(indexBytes, &ociIndex); err != nil {
		return Index{}, Descriptor{}, fmt.Errorf("parse index: %w", err)
	}

	index := Index{
		ArtifactType: ociIndex.ArtifactType,
		Annotations:  ociIndex.Annotations,
	}
	for _, m := range ociIndex.Manifests {
		index.Manifests = append(index.Manifests, IndexManifest{
			Descriptor: Descriptor{
				MediaType:    m.MediaType,
				ArtifactType: m.ArtifactType,
				Digest:       m.Digest.String(),
				Size:         m.Size,
				Annotations:  m.Annotations,
			},
		})
	}

	desc := Descriptor{
		MediaType:    indexDesc.MediaType,
		ArtifactType: ociIndex.ArtifactType,
		Digest:       indexDesc.Digest.String(),
		Size:         indexDesc.Size,
		Annotations:  ociIndex.Annotations,
	}

	return index, desc, nil
}
