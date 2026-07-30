package registry

import (
	"bytes"
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

// PushArtifact uploads an Artifact to the registry at ref. The ref must
// include a tag.
//
// The flow is: stage every blob in an in-memory store, pack the manifest
// against those staged descriptors, tag it, then copy store→registry in
// one pass. Staging first means a malformed artifact fails before anything
// leaves the process, and the copy sees a complete, self-consistent
// manifest rather than a stream of partial state.
func (c *Client) PushArtifact(ctx context.Context, ref string, art Artifact) (Descriptor, error) {
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

	store := memory.New()

	layers := make([]ocispec.Descriptor, 0, len(art.Layers))
	for _, layer := range art.Layers {
		desc := ocispec.Descriptor{
			MediaType: layer.MediaType,
			Digest:    digest.FromBytes(layer.Data),
			Size:      int64(len(layer.Data)),
		}
		if err := store.Push(ctx, desc, bytes.NewReader(layer.Data)); err != nil {
			return Descriptor{}, fmt.Errorf("stage layer: %w", err)
		}
		layers = append(layers, desc)
	}

	packOpts := oras.PackManifestOptions{
		Layers:              layers,
		ManifestAnnotations: art.Annotations,
	}
	if art.Config.MediaType != "" {
		configDesc := ocispec.Descriptor{
			MediaType: art.Config.MediaType,
			Digest:    digest.FromBytes(art.Config.Data),
			Size:      int64(len(art.Config.Data)),
		}
		if err := store.Push(ctx, configDesc, bytes.NewReader(art.Config.Data)); err != nil {
			return Descriptor{}, fmt.Errorf("stage config: %w", err)
		}
		packOpts.ConfigDescriptor = &configDesc
	}

	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, art.ArtifactType, packOpts)
	if err != nil {
		return Descriptor{}, fmt.Errorf("pack manifest: %w", err)
	}

	if err := store.Tag(ctx, manifestDesc, parsed.Tag); err != nil {
		return Descriptor{}, fmt.Errorf("tag: %w", err)
	}

	if _, err := oras.Copy(ctx, store, parsed.Tag, repo, parsed.Tag, oras.DefaultCopyOptions); err != nil {
		return Descriptor{}, mapError(err, parsed)
	}

	artifactType := manifestDesc.ArtifactType
	if artifactType == "" {
		artifactType = art.ArtifactType
	}

	return Descriptor{
		MediaType:    manifestDesc.MediaType,
		ArtifactType: artifactType,
		Digest:       manifestDesc.Digest.String(),
		Size:         manifestDesc.Size,
		Annotations:  manifestDesc.Annotations,
	}, nil
}
