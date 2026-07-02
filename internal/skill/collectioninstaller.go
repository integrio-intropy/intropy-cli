package skill

import (
	"context"
	"fmt"
	"time"
)

// CollectionInstaller registers a collection on a project and installs every
// skill the collection index lists.
type CollectionInstaller struct {
	registry Registry
	adder    *Adder
	project  *Project
}

func NewCollectionInstaller(r Registry, a *Adder, p *Project) *CollectionInstaller {
	return &CollectionInstaller{registry: r, adder: a, project: p}
}

// InstallAll fetches the collection index at ref, registers the collection on
// the project under alias (caching the index for later name lookups), and
// installs every skill the index lists. The index is fetched before
// skills.json is touched, so a bad ref leaves the project unchanged.
func (c *CollectionInstaller) InstallAll(ctx context.Context, alias, ref string) ([]LockEntry, error) {
	index, err := c.registry.PullIndex(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("fetch index %s: %w", ref, err)
	}

	manifest, err := c.project.LoadManifest()
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	registered := false
	for _, col := range manifest.Collections {
		if col.Name == alias {
			registered = true
			break
		}
	}
	if !registered {
		manifest.Collections = append(manifest.Collections, ManifestCollection{Name: alias, Ref: ref})
		if err := c.project.SaveManifest(manifest); err != nil {
			return nil, fmt.Errorf("save manifest: %w", err)
		}
	}
	cached := &CachedCollection{Ref: ref, FetchedAt: time.Now().UTC(), Index: index}
	if err := c.project.SaveCollectionCache(alias, cached); err != nil {
		return nil, fmt.Errorf("cache index: %w", err)
	}

	entries := make([]LockEntry, 0, len(index.Manifests))
	for _, s := range index.Manifests {
		if s.Ref == "" {
			return entries, fmt.Errorf("collection entry %q has no ref annotation", s.Name)
		}
		entry, err := c.adder.Add(ctx, s.Ref, AddOptions{})
		if err != nil {
			return entries, fmt.Errorf("add %s: %w", s.Name, err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}
