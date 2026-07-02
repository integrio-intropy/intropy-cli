package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

// CollectionSpec is the YAML shape consumed by `intropy skills collection publish`.
type CollectionSpec struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	License     string      `yaml:"license,omitempty"`
	Source      string      `yaml:"source,omitempty"`
	Skills      []SpecSkill `yaml:"skills"`
}

type SpecSkill struct {
	Ref string `yaml:"ref"`
}

// CollectionPublisher resolves each spec entry to a digest, builds an OCI
// Image Index, and pushes it to the registry.
type CollectionPublisher struct {
	registry Registry
}

func NewCollectionPublisher(r Registry) *CollectionPublisher {
	return &CollectionPublisher{registry: r}
}

func (p *CollectionPublisher) Publish(
	ctx context.Context,
	spec *CollectionSpec,
	collectionRef string,
	force bool,
) (oci.Descriptor, error) {
	parsed, err := oci.ParseReference(collectionRef)
	if err != nil {
		return oci.Descriptor{}, fmt.Errorf("parse collection ref: %w", err)
	}
	if parsed.Tag == "" {
		return oci.Descriptor{}, fmt.Errorf("collection ref must include a tag")
	}

	if !force {
		if _, err := p.registry.PullIndex(ctx, collectionRef); err == nil {
			return oci.Descriptor{}, fmt.Errorf("collection tag %s already exists; use --force to overwrite", collectionRef)
		} else if !errors.Is(err, oci.ErrNotFound) {
			return oci.Descriptor{}, fmt.Errorf("pre-flight check: %w", err)
		}
	}

	entries := make([]oci.IndexEntry, 0, len(spec.Skills))
	for _, s := range spec.Skills {
		desc, err := p.registry.Resolve(ctx, s.Ref)
		if err != nil {
			return oci.Descriptor{}, fmt.Errorf("resolve %s: %w", s.Ref, err)
		}
		entries = append(entries, oci.IndexEntry{
			Name:        desc.Annotations[oci.AnnotationSkillName],
			Ref:         s.Ref,
			Version:     desc.Annotations["org.opencontainers.image.version"],
			Description: desc.Annotations["org.opencontainers.image.description"],
			Digest:      desc.Digest,
			Size:        desc.Size,
		})
	}

	index := oci.Index{
		Annotations: buildCollectionAnnotations(spec),
		Manifests:   entries,
	}

	return p.registry.PushIndex(ctx, collectionRef, index)
}

func buildCollectionAnnotations(spec *CollectionSpec) map[string]string {
	a := map[string]string{
		oci.AnnotationCollectionName:           spec.Name,
		"org.opencontainers.image.title":       spec.Name,
		"org.opencontainers.image.description": spec.Description,
	}
	if spec.License != "" {
		a["org.opencontainers.image.licenses"] = spec.License
	}
	if spec.Source != "" {
		a["org.opencontainers.image.source"] = spec.Source
	}
	return a
}
