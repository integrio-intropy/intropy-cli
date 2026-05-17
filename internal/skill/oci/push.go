package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

// Push uploads a packed skill Artifact to the registry at ref. The ref must
// include a tag; that tag becomes the skill's version in the published config
// (spec §4.2).
func (c *Client) Push(ctx context.Context, ref string, art Artifact) (Descriptor, error) {
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

	art.Config.Version = parsed.Tag

	layerBytes, err := io.ReadAll(art.Content)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read layer: %w", err)
	}

	store := memory.New()

	layerDesc := ocispec.Descriptor{
		MediaType: MediaTypeSkillContent,
		Digest:    digest.FromBytes(layerBytes),
		Size:      int64(len(layerBytes)),
	}
	if err := store.Push(ctx, layerDesc, bytes.NewReader(layerBytes)); err != nil {
		return Descriptor{}, fmt.Errorf("stage layer: %w", err)
	}

	configBytes, err := json.Marshal(art.Config)
	if err != nil {
		return Descriptor{}, fmt.Errorf("marshal config: %w", err)
	}
	configDesc := ocispec.Descriptor{
		MediaType: MediaTypeSkillConfig,
		Digest:    digest.FromBytes(configBytes),
		Size:      int64(len(configBytes)),
	}
	if err := store.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil {
		return Descriptor{}, fmt.Errorf("stage config: %w", err)
	}

	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, MediaTypeSkillArtifact,
		oras.PackManifestOptions{
			Layers:              []ocispec.Descriptor{layerDesc},
			ConfigDescriptor:    &configDesc,
			ManifestAnnotations: buildSkillAnnotations(art.Config),
		})
	if err != nil {
		return Descriptor{}, fmt.Errorf("pack manifest: %w", err)
	}

	if err := store.Tag(ctx, manifestDesc, parsed.Tag); err != nil {
		return Descriptor{}, fmt.Errorf("tag: %w", err)
	}

	if _, err := oras.Copy(ctx, store, parsed.Tag, repo, parsed.Tag, oras.DefaultCopyOptions); err != nil {
		return Descriptor{}, mapError(err, ref)
	}

	return Descriptor{
		MediaType:    manifestDesc.MediaType,
		ArtifactType: MediaTypeSkillArtifact,
		Digest:       manifestDesc.Digest.String(),
		Size:         manifestDesc.Size,
		Annotations:  manifestDesc.Annotations,
	}, nil
}

func buildSkillAnnotations(cfg Config) map[string]string {
	a := map[string]string{
		AnnotationSkillName:                    cfg.Name,
		"org.opencontainers.image.title":       cfg.Name,
		"org.opencontainers.image.description": cfg.Description,
		"org.opencontainers.image.version":     cfg.Version,
		"org.opencontainers.image.created":     time.Now().UTC().Format(time.RFC3339),
	}
	if cfg.License != "" {
		a["org.opencontainers.image.licenses"] = cfg.License
	}
	return a
}
