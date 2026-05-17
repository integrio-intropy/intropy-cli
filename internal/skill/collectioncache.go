package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/intropy/intropy-cli/internal/skill/oci"
)

// CachedCollection persists a fetched collection index for offline name lookup.
type CachedCollection struct {
	Ref       string    `json:"ref"`
	FetchedAt time.Time `json:"fetchedAt"`
	Index     oci.Index `json:"index"`
}

// SkillResolution is the result of looking up a skill name across registered collections.
type SkillResolution struct {
	Collection string
	Entry      oci.IndexEntry
}

// CollectionCacheDir returns the directory where collection caches are stored.
func (p *Project) CollectionCacheDir() string {
	return filepath.Join(p.Root, ".intropy", "collections")
}

// CollectionCachePath returns the cache file path for a named collection.
func (p *Project) CollectionCachePath(name string) string {
	return filepath.Join(p.CollectionCacheDir(), name+".json")
}

// LoadCollectionCache reads and parses the cache file for a collection alias.
// Returns an os.ErrNotExist-wrapping error if the cache hasn't been fetched yet.
func (p *Project) LoadCollectionCache(name string) (*CachedCollection, error) {
	data, err := os.ReadFile(p.CollectionCachePath(name))
	if err != nil {
		return nil, err
	}
	var c CachedCollection
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse cache for %q: %w", name, err)
	}
	return &c, nil
}

// SaveCollectionCache persists the cache file for a collection alias, creating
// the cache directory if needed.
func (p *Project) SaveCollectionCache(name string, c *CachedCollection) error {
	if err := os.MkdirAll(p.CollectionCacheDir(), 0755); err != nil {
		return fmt.Errorf("mkdir cache dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(p.CollectionCachePath(name), data, 0644)
}

// ResolveSkillName searches every registered collection's cache for a skill
// matching name. If collectionAlias is non-empty, only that collection is
// considered. Returns an error if zero or multiple matches are found.
func ResolveSkillName(p *Project, name, collectionAlias string) (SkillResolution, error) {
	manifest, err := p.LoadManifest()
	if err != nil {
		return SkillResolution{}, fmt.Errorf("load manifest: %w", err)
	}

	if len(manifest.Collections) == 0 {
		return SkillResolution{}, fmt.Errorf("no collections registered; run `intropy skills collection add` first")
	}

	if collectionAlias != "" {
		known := false
		for _, c := range manifest.Collections {
			if c.Name == collectionAlias {
				known = true
				break
			}
		}
		if !known {
			return SkillResolution{}, fmt.Errorf("no collection registered as %q", collectionAlias)
		}
	}

	var matches []SkillResolution
	var missingCache []string
	for _, coll := range manifest.Collections {
		if collectionAlias != "" && coll.Name != collectionAlias {
			continue
		}
		cached, err := p.LoadCollectionCache(coll.Name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missingCache = append(missingCache, coll.Name)
				continue
			}
			return SkillResolution{}, fmt.Errorf("load cache for %q: %w", coll.Name, err)
		}
		for _, entry := range cached.Index.Manifests {
			if entry.Name == name {
				matches = append(matches, SkillResolution{
					Collection: coll.Name,
					Entry:      entry,
				})
			}
		}
	}

	switch {
	case len(matches) == 0:
		if len(missingCache) > 0 {
			return SkillResolution{}, fmt.Errorf(
				"skill %q not found; collections without a cache: %v (re-run `intropy skills collection add` to refresh)",
				name, missingCache,
			)
		}
		if collectionAlias != "" {
			return SkillResolution{}, fmt.Errorf("skill %q not found in collection %q", name, collectionAlias)
		}
		return SkillResolution{}, fmt.Errorf("skill %q not found in any registered collection", name)
	case len(matches) > 1:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Collection)
		}
		return SkillResolution{}, fmt.Errorf(
			"skill %q found in multiple collections %v; use --collection to disambiguate",
			name, names,
		)
	}

	return matches[0], nil
}
