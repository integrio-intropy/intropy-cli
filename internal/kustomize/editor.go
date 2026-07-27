// Package kustomize drives the kustomize binary and reads the parts of a
// kustomization file the deployment commands care about, plus the normalisation
// and diffing that make two renders comparable.
package kustomize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"gopkg.in/yaml.v3"
)

// KustomizationFileNames are the names kustomize accepts, in the order it
// looks for them.
var KustomizationFileNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// AnnotationSourceCommit records which source commit an overlay was pinned
// from. Read back by `deploy status` and `deploy history`.
const AnnotationSourceCommit = "deploy.internal/source-commit"

// AnnotationRelease records which release version an overlay was pinned from,
// absent when the digests came from a commit rather than a release.
//
// Read back by `deploy promote`, which is the reason it exists: promotion
// copies digests, so without this the version staging runs would be knowable
// only from a commit trailer. A promotion cannot name what it is promoting
// otherwise.
const AnnotationRelease = "deploy.internal/release"

// Client drives the kustomize binary against an overlay directory.
type Client struct {
	Runner command.Runner
}

// Build renders the overlay and returns the manifest stream.
func (k Client) Build(ctx context.Context, dir string) ([]byte, error) {
	stdout, _, err := k.Runner.Run(ctx, dir, "kustomize", "build", ".")
	if err != nil {
		return nil, fmt.Errorf("kustomize build %s: %w", dir, err)
	}
	return stdout, nil
}

// SetImage pins an image to a digest.
//
// kustomize rewrites the overlay's images[] entry, replacing any newTag with a
// digest field — which is what makes this the right tool rather than a hand
// edit: it also handles the entry not existing yet.
func (k Client) SetImage(ctx context.Context, dir, image, digest string) error {
	if _, _, err := k.Runner.Run(ctx, dir, "kustomize", "edit", "set", "image", image+"@"+digest); err != nil {
		return fmt.Errorf("pin %s: %w", image, err)
	}
	return nil
}

// SetAnnotation sets a common annotation.
//
// This writes commonAnnotations, which kustomize propagates onto every
// resource *including pod templates* — verified against kustomize v5. Changing
// the source-commit annotation therefore rolls the pods even when the image
// digest is unchanged. That is deliberate: an annotation named source-commit
// which does not track the source commit would be worse than a rollout, so
// callers announce a provenance-only change rather than suppress it.
func (k Client) SetAnnotation(ctx context.Context, dir, key, value string) error {
	if _, _, err := k.Runner.Run(ctx, dir, "kustomize", "edit", "set", "annotation", key+":"+value); err != nil {
		return fmt.Errorf("set annotation %s: %w", key, err)
	}
	return nil
}

// RemoveAnnotation deletes a common annotation.
//
// Callers should check that the key is present first, using the Kustomization
// they have already read: whether kustomize treats removing an absent
// annotation as an error is its business, and depending on the answer either
// way would be fragile.
func (k Client) RemoveAnnotation(ctx context.Context, dir, key string) error {
	if _, _, err := k.Runner.Run(ctx, dir, "kustomize", "edit", "remove", "annotation", key); err != nil {
		return fmt.Errorf("remove annotation %s: %w", key, err)
	}
	return nil
}

// Kustomization is the subset of an overlay's kustomization.yaml this package
// reads. It is deliberately partial and never written back — kustomize owns
// writes, so unmodelled fields cannot be lost.
type Kustomization struct {
	Images            []KustomizeImage  `yaml:"images"`
	CommonAnnotations map[string]string `yaml:"commonAnnotations"`
}

// KustomizeImage is one images[] entry.
type KustomizeImage struct {
	Name    string `yaml:"name"`
	NewName string `yaml:"newName"`
	NewTag  string `yaml:"newTag"`
	Digest  string `yaml:"digest"`
}

// Pinned reports how the image is currently pinned, for display: a digest, a
// tag, or nothing.
func (i KustomizeImage) Pinned() string {
	switch {
	case i.Digest != "":
		return i.Digest
	case i.NewTag != "":
		return ":" + i.NewTag
	default:
		return "(unpinned)"
	}
}

// KustomizationPath returns the overlay's kustomization file.
func KustomizationPath(dir string) (string, error) {
	for _, name := range KustomizationFileNames {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s has no kustomization file (looked for %v)", dir, KustomizationFileNames)
}

// ReadKustomization parses the overlay's kustomization file.
func ReadKustomization(dir string) (*Kustomization, string, error) {
	path, err := KustomizationPath(dir)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	var k Kustomization
	if err := yaml.Unmarshal(data, &k); err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", path, err)
	}
	return &k, path, nil
}

// FindImage returns the images[] entry for a repository name.
func (k *Kustomization) FindImage(name string) (KustomizeImage, bool) {
	for _, img := range k.Images {
		if img.Name == name {
			return img, true
		}
	}
	return KustomizeImage{}, false
}
