package registry

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

// PullArtifact fetches the manifest at ref along with its config and layer
// blobs. It applies no policy: callers decide which artifact types and
// layouts are acceptable. The returned Descriptor describes the manifest.
func (c *Client) PullArtifact(ctx context.Context, ref string) (Artifact, Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Artifact{}, Descriptor{}, fmt.Errorf("parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Artifact{}, Descriptor{}, err
	}

	store := memory.New()
	target := parsed.TagOrDigest()

	manifestDesc, err := oras.Copy(ctx, repo, target, store, target, oras.DefaultCopyOptions)
	if err != nil {
		return Artifact{}, Descriptor{}, mapError(err, parsed)
	}

	manifestBytes, err := content.FetchAll(ctx, store, manifestDesc)
	if err != nil {
		return Artifact{}, Descriptor{}, fmt.Errorf("fetch manifest: %w", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Artifact{}, Descriptor{}, fmt.Errorf("parse manifest: %w", err)
	}

	configBytes, err := content.FetchAll(ctx, store, manifest.Config)
	if err != nil {
		return Artifact{}, Descriptor{}, fmt.Errorf("fetch config: %w", err)
	}

	art := Artifact{
		ArtifactType: manifest.ArtifactType,
		Config:       Blob{MediaType: manifest.Config.MediaType, Data: configBytes},
		Annotations:  manifest.Annotations,
	}
	for _, layer := range manifest.Layers {
		layerBytes, err := content.FetchAll(ctx, store, layer)
		if err != nil {
			return Artifact{}, Descriptor{}, fmt.Errorf("fetch layer: %w", err)
		}
		art.Layers = append(art.Layers, Blob{MediaType: layer.MediaType, Data: layerBytes})
	}

	desc := Descriptor{
		MediaType:    manifestDesc.MediaType,
		ArtifactType: manifest.ArtifactType,
		Digest:       manifestDesc.Digest.String(),
		Size:         manifestDesc.Size,
		Annotations:  manifest.Annotations,
	}

	return art, desc, nil
}
