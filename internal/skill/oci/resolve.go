package oci

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

func (c *Client) Resolve(ctx context.Context, ref string) (Descriptor, error) {
	parsed, err := ParseReference(ref)
	if err != nil {
		return Descriptor{}, fmt.Errorf("Parse ref: %w", err)
	}

	repo, err := c.repository(parsed)
	if err != nil {
		return Descriptor{}, err
	}

	target := parsed.Tag
	if target == "" {
		target = parsed.Digest
	}

	desc, err := repo.Resolve(ctx, ref)
	if err != nil {
		return Descriptor{}, mapError(err, ref)
	}

	manifestBytes, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return Descriptor{}, fmt.Errorf("fetch manifest %s: %w", ref, err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Descriptor{}, fmt.Errorf("parse manifest %s: %w", ref, err)
	}

	return Descriptor{
		MediaType:    desc.MediaType,
		ArtifactType: desc.ArtifactType,
		Digest:       desc.Digest.String(),
		Size:         desc.Size,
		Annotations:  manifest.Annotations,
	}, nil
}
