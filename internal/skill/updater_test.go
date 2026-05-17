package skill

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/intropy/intropy-cli/internal/skill/oci"
)

func TestUpdaterNoChange(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	// Manifest with skill at version 1.0.0
	manifest := &Manifest{
		Collections: []ManifestCollection{
			{Name: "example", Ref: "ghcr.io/example/index:latest"},
		},
		Skills: []ManifestEntry{
			{Name: "pr-review", Source: "ghcr.io/example/pr-review", Version: "1.0.0"},
		},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	// Collection cache resolves to the SAME version
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

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	installer := NewInstaller(reg, &mockExtractor{}, p)
	updater := NewUpdater(reg, installer, p)

	result, err := updater.Update(context.Background(), "pr-review")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Changed {
		t.Error("expected Changed=false")
	}
	if result.OldVersion != "1.0.0" {
		t.Errorf("OldVersion: got %q, want 1.0.0", result.OldVersion)
	}
}

func TestUpdaterChanges(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	// Manifest with skill at version 1.0.0
	manifest := &Manifest{
		Collections: []ManifestCollection{
			{Name: "example", Ref: "ghcr.io/example/index:latest"},
		},
		Skills: []ManifestEntry{
			{Name: "pr-review", Source: "ghcr.io/example/pr-review", Version: "1.0.0"},
		},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	// Collection cache resolves to a NEW version
	cache := &CachedCollection{
		Ref: "ghcr.io/example/index:latest",
		Index: oci.Index{
			Manifests: []oci.IndexEntry{
				{Name: "pr-review", Ref: "ghcr.io/example/pr-review:1.1.0"},
			},
		},
	}
	if err := p.SaveCollectionCache("example", cache); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	installer := NewInstaller(reg, &mockExtractor{}, p)
	updater := NewUpdater(reg, installer, p)

	result, err := updater.Update(context.Background(), "pr-review")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed=true")
	}
	if result.OldVersion != "1.0.0" {
		t.Errorf("OldVersion: got %q, want 1.0.0", result.OldVersion)
	}
	if result.NewVersion != "1.1.0" {
		t.Errorf("NewVersion: got %q, want 1.1.0", result.NewVersion)
	}

	// Manifest should be updated
	updatedManifest, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if updatedManifest.Skills[0].Version != "1.1.0" {
		t.Errorf("manifest version: got %q, want 1.1.0", updatedManifest.Skills[0].Version)
	}
}

func TestUpdaterSkillNotInManifest(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	manifest := &Manifest{
		Collections: []ManifestCollection{},
		Skills:      []ManifestEntry{},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{}
	installer := NewInstaller(reg, &mockExtractor{}, p)
	updater := NewUpdater(reg, installer, p)

	_, err := updater.Update(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not in the manifest") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}

func TestUpdaterInstallFails(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	manifest := &Manifest{
		Collections: []ManifestCollection{
			{Name: "example", Ref: "ghcr.io/example/index:latest"},
		},
		Skills: []ManifestEntry{
			{Name: "pr-review", Source: "ghcr.io/example/pr-review", Version: "1.0.0"},
		},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	cache := &CachedCollection{
		Ref: "ghcr.io/example/index:latest",
		Index: oci.Index{
			Manifests: []oci.IndexEntry{
				{Name: "pr-review", Ref: "ghcr.io/example/pr-review:1.1.0"},
			},
		},
	}
	if err := p.SaveCollectionCache("example", cache); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	extr := &mockExtractor{extractErr: fmt.Errorf("disk full")}
	installer := NewInstaller(reg, extr, p)
	updater := NewUpdater(reg, installer, p)

	_, err := updater.Update(context.Background(), "pr-review")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}
