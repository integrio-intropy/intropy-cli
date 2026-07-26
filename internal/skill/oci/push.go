package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
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

	art.Config.Version = parsed.Tag

	layerBytes, err := io.ReadAll(art.Content)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read layer: %w", err)
	}

	configBytes, err := json.Marshal(art.Config)
	if err != nil {
		return Descriptor{}, fmt.Errorf("marshal config: %w", err)
	}

	return c.reg.PushArtifact(ctx, ref, registry.Artifact{
		ArtifactType: MediaTypeSkillArtifact,
		Config:       registry.Blob{MediaType: MediaTypeSkillConfig, Data: configBytes},
		Layers:       []registry.Blob{{MediaType: MediaTypeSkillContent, Data: layerBytes}},
		Annotations:  buildSkillAnnotations(art.Config),
	})
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
