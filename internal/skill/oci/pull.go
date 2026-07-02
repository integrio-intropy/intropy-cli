package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
)

func (c *Client) Pull(ctx context.Context, ref string) (Artifact, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Artifact{}, fmt.Errorf("parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Artifact{}, fmt.Errorf("parse ref: %w", err)
	}

	store := memory.New()
	tagOrDigest := parsed.Tag
	if tagOrDigest == "" {
		tagOrDigest = parsed.Digest
	}

	manifestDesc, err := oras.Copy(ctx, repo, tagOrDigest, store, tagOrDigest, oras.DefaultCopyOptions)
	if err != nil {
		return Artifact{}, mapError(err, ref)
	}

	manifestBytes, err := content.FetchAll(ctx, store, manifestDesc)
	if err != nil {
		return Artifact{}, fmt.Errorf("fetch manifest: %w", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Artifact{}, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.ArtifactType != MediaTypeSkillArtifact {
		return Artifact{}, fmt.Errorf("%w got artifactType %q", ErrNotSkill, manifest.ArtifactType)
	}

	configBytes, err := content.FetchAll(ctx, store, manifest.Config)
	if err != nil {
		return Artifact{}, fmt.Errorf("fetch config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return Artifact{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Artifact{}, fmt.Errorf("invalid skill config: %w", err)
	}

	if len(manifest.Layers) != 1 {
		return Artifact{}, fmt.Errorf("%w expected 1 layer, got %d", ErrNotSkill, len(manifest.Layers))
	}

	layer := manifest.Layers[0]
	if layer.MediaType != MediaTypeSkillContent {
		return Artifact{}, fmt.Errorf("%w unexpected layer media type %q", ErrNotSkill, layer.MediaType)
	}

	layerBytes, err := content.FetchAll(ctx, store, layer)
	if err != nil {
		return Artifact{}, fmt.Errorf("fetch layer: %w", err)
	}

	return Artifact{
		Config:  cfg,
		Content: io.NopCloser(bytes.NewReader(layerBytes)),
		Digest:  manifestDesc.Digest.String(),
		Tag:     parsed.Tag,
	}, nil

}

func (c *Client) repository(ref Reference) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.Registry + "/" + ref.Repository)
	if err != nil {
		return nil, fmt.Errorf("build repository client: %w", err)
	}

	repo.Client = c.auth
	// Same convention as docker and the oras CLI: local registries speak
	// plain HTTP without an explicit opt-in.
	repo.PlainHTTP = isLocalRegistry(ref.Registry)

	return repo, nil
}

func isLocalRegistry(registry string) bool {
	host := registry
	if h, _, err := net.SplitHostPort(registry); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func mapError(err error, ref string) error {
	switch {
	case errors.Is(err, errdef.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, ref)
	default:
		return err
	}
}
