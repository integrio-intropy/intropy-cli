package skill

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

func TestCollectionCacheRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	original := &CachedCollection{
		Ref:       "ghcr.io/example/index:latest",
		FetchedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Index: oci.Index{
			Annotations: map[string]string{"name": "example"},
			Manifests: []oci.IndexEntry{
				{Name: "pr-review", Ref: "ghcr.io/example/pr-review:1.0.0"},
			},
		},
	}

	if err := p.SaveCollectionCache("example", original); err != nil {
		t.Fatalf("SaveCollectionCache: %v", err)
	}

	loaded, err := p.LoadCollectionCache("example")
	if err != nil {
		t.Fatalf("LoadCollectionCache: %v", err)
	}

	if loaded.Ref != original.Ref {
		t.Errorf("Ref: got %q, want %q", loaded.Ref, original.Ref)
	}
	if len(loaded.Index.Manifests) != 1 || loaded.Index.Manifests[0].Name != "pr-review" {
		t.Errorf("Manifests: got %+v", loaded.Index.Manifests)
	}
}

func TestLoadCollectionCacheNotFound(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	_, err := p.LoadCollectionCache("nonexistent")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got: %v", err)
	}
}

func TestResolveSkillName(t *testing.T) {
	t.Run("found in single collection", func(t *testing.T) {
		tmp := t.TempDir()
		p := &Project{Root: tmp}

		manifest := &Manifest{
			Collections: []ManifestCollection{
				{Name: "example", Ref: "ghcr.io/example/index:latest"},
			},
			Skills: []ManifestEntry{},
		}
		if err := p.SaveManifest(manifest); err != nil {
			t.Fatal(err)
		}

		cache := &CachedCollection{
			Ref: "ghcr.io/example/index:latest",
			Index: oci.Index{
				Manifests: []oci.IndexEntry{
					{Name: "pr-review", Ref: "ghcr.io/example/pr-review:1.0.0"},
				},
			},
		}
		if err := p.SaveCollectionCache("example", cache); err != nil {
			t.Fatal(err)
		}

		res, err := ResolveSkillName(p, "pr-review", "")
		if err != nil {
			t.Fatalf("ResolveSkillName: %v", err)
		}
		if res.Collection != "example" {
			t.Errorf("Collection: got %q, want example", res.Collection)
		}
		if res.Entry.Ref != "ghcr.io/example/pr-review:1.0.0" {
			t.Errorf("Entry.Ref: got %q", res.Entry.Ref)
		}
	})

	t.Run("no collections registered", func(t *testing.T) {
		tmp := t.TempDir()
		p := &Project{Root: tmp}
		if err := p.SaveManifest(&Manifest{Skills: []ManifestEntry{}}); err != nil {
			t.Fatal(err)
		}

		_, err := ResolveSkillName(p, "pr-review", "")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("skill not found", func(t *testing.T) {
		tmp := t.TempDir()
		p := &Project{Root: tmp}

		manifest := &Manifest{
			Collections: []ManifestCollection{
				{Name: "example", Ref: "ghcr.io/example/index:latest"},
			},
			Skills: []ManifestEntry{},
		}
		if err := p.SaveManifest(manifest); err != nil {
			t.Fatal(err)
		}

		cache := &CachedCollection{
			Ref:   "ghcr.io/example/index:latest",
			Index: oci.Index{Manifests: []oci.IndexEntry{}},
		}
		if err := p.SaveCollectionCache("example", cache); err != nil {
			t.Fatal(err)
		}

		_, err := ResolveSkillName(p, "missing", "")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("ambiguous without --collection", func(t *testing.T) {
		tmp := t.TempDir()
		p := &Project{Root: tmp}

		manifest := &Manifest{
			Collections: []ManifestCollection{
				{Name: "a", Ref: "ghcr.io/a/index:latest"},
				{Name: "b", Ref: "ghcr.io/b/index:latest"},
			},
			Skills: []ManifestEntry{},
		}
		if err := p.SaveManifest(manifest); err != nil {
			t.Fatal(err)
		}

		for _, name := range []string{"a", "b"} {
			cache := &CachedCollection{
				Ref: "ghcr.io/" + name + "/index:latest",
				Index: oci.Index{
					Manifests: []oci.IndexEntry{
						{Name: "pr-review", Ref: "ghcr.io/" + name + "/pr-review:1.0.0"},
					},
				},
			}
			if err := p.SaveCollectionCache(name, cache); err != nil {
				t.Fatal(err)
			}
		}

		_, err := ResolveSkillName(p, "pr-review", "")
		if err == nil {
			t.Fatal("expected error for ambiguous match")
		}
	})

	t.Run("disambiguated with --collection", func(t *testing.T) {
		tmp := t.TempDir()
		p := &Project{Root: tmp}

		manifest := &Manifest{
			Collections: []ManifestCollection{
				{Name: "a", Ref: "ghcr.io/a/index:latest"},
				{Name: "b", Ref: "ghcr.io/b/index:latest"},
			},
			Skills: []ManifestEntry{},
		}
		if err := p.SaveManifest(manifest); err != nil {
			t.Fatal(err)
		}

		for _, name := range []string{"a", "b"} {
			cache := &CachedCollection{
				Ref: "ghcr.io/" + name + "/index:latest",
				Index: oci.Index{
					Manifests: []oci.IndexEntry{
						{Name: "pr-review", Ref: "ghcr.io/" + name + "/pr-review:1.0.0"},
					},
				},
			}
			if err := p.SaveCollectionCache(name, cache); err != nil {
				t.Fatal(err)
			}
		}

		res, err := ResolveSkillName(p, "pr-review", "a")
		if err != nil {
			t.Fatalf("ResolveSkillName: %v", err)
		}
		if res.Collection != "a" {
			t.Errorf("Collection: got %q, want a", res.Collection)
		}
	})
}
