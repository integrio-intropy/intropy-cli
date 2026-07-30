package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Pull fetches a skill artifact at ref and validates it against the skills
// spec: the artifactType must be the skill type, the config must parse and
// validate, and there must be exactly one content layer of the skill
// tar+gzip type. Any deviation is reported as ErrNotSkill — pulling
// something that is not a skill is a caller error, not a partial result.
func (c *Client) Pull(ctx context.Context, ref string) (Artifact, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Artifact{}, fmt.Errorf("parse ref: %w", err)
	}

	art, desc, err := c.reg.PullArtifact(ctx, ref)
	if err != nil {
		return Artifact{}, err
	}

	if art.ArtifactType != MediaTypeSkillArtifact {
		return Artifact{}, fmt.Errorf("%w got artifactType %q", ErrNotSkill, art.ArtifactType)
	}

	var cfg Config
	if err := json.Unmarshal(art.Config.Data, &cfg); err != nil {
		return Artifact{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Artifact{}, fmt.Errorf("invalid skill config: %w", err)
	}

	if len(art.Layers) != 1 {
		return Artifact{}, fmt.Errorf("%w expected 1 layer, got %d", ErrNotSkill, len(art.Layers))
	}

	layer := art.Layers[0]
	if layer.MediaType != MediaTypeSkillContent {
		return Artifact{}, fmt.Errorf("%w unexpected layer media type %q", ErrNotSkill, layer.MediaType)
	}

	return Artifact{
		Config:  cfg,
		Content: io.NopCloser(bytes.NewReader(layer.Data)),
		Digest:  desc.Digest,
		Tag:     parsed.Tag,
	}, nil
}
