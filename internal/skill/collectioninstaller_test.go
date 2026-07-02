package skill

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

func collectionProject(t *testing.T) *Project {
	t.Helper()
	p := &Project{Root: t.TempDir()}
	if err := p.SaveManifest(&Manifest{Skills: []ManifestEntry{}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func collectionInstaller(reg *mockRegistry, p *Project) *CollectionInstaller {
	installer := NewInstaller(reg, &mockExtractor{}, p)
	return NewCollectionInstaller(reg, NewAdder(reg, installer, p), p)
}

func TestCollectionInstallerSuccess(t *testing.T) {
	p := collectionProject(t)
	reg := &mockRegistry{
		pullIndex: oci.Index{Manifests: []oci.IndexEntry{
			{Name: "pr-review", Ref: "ghcr.io/example/skills/pr-review:1.0.0"},
			{Name: "docs", Ref: "ghcr.io/example/skills/docs:2.0.0"},
		}},
		pullByRef: map[string]oci.Artifact{
			"ghcr.io/example/skills/pr-review:1.0.0": artifactWithName("pr-review"),
			"ghcr.io/example/skills/docs:2.0.0":      artifactWithName("docs"),
		},
	}

	entries, err := collectionInstaller(reg, p).InstallAll(context.Background(), "intropy", "ghcr.io/example/collection:latest")
	if err != nil {
		t.Fatalf("InstallAll: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 lock entries, got %d", len(entries))
	}

	manifest, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Collections) != 1 || manifest.Collections[0].Name != "intropy" {
		t.Errorf("collections: got %+v", manifest.Collections)
	}
	if len(manifest.Skills) != 2 {
		t.Errorf("skills: got %+v", manifest.Skills)
	}

	cached, err := p.LoadCollectionCache("intropy")
	if err != nil {
		t.Fatalf("expected cached index: %v", err)
	}
	if len(cached.Index.Manifests) != 2 {
		t.Errorf("cached index: got %+v", cached.Index.Manifests)
	}

	lockfile, err := p.LoadLockfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(lockfile.Skills) != 2 {
		t.Errorf("lockfile: got %+v", lockfile.Skills)
	}
}

func TestCollectionInstallerIndexFetchFails(t *testing.T) {
	p := collectionProject(t)
	reg := &mockRegistry{pullIndexErr: fmt.Errorf("network error")}

	_, err := collectionInstaller(reg, p).InstallAll(context.Background(), "intropy", "ghcr.io/example/collection:latest")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Errorf("error %q did not surface the fetch failure", err.Error())
	}

	// A bad ref must leave skills.json untouched.
	manifest, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Collections) != 0 {
		t.Errorf("collections should be empty, got %+v", manifest.Collections)
	}
}

func TestCollectionInstallerEntryMissingRef(t *testing.T) {
	p := collectionProject(t)
	reg := &mockRegistry{
		pullIndex: oci.Index{Manifests: []oci.IndexEntry{{Name: "pr-review"}}},
	}

	_, err := collectionInstaller(reg, p).InstallAll(context.Background(), "intropy", "ghcr.io/example/collection:latest")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no ref annotation") {
		t.Errorf("error %q did not mention the missing ref", err.Error())
	}
}

func TestCollectionInstallerAlreadyRegistered(t *testing.T) {
	p := &Project{Root: t.TempDir()}
	if err := p.SaveManifest(&Manifest{
		Collections: []ManifestCollection{{Name: "intropy", Ref: "ghcr.io/example/collection:latest"}},
		Skills:      []ManifestEntry{},
	}); err != nil {
		t.Fatal(err)
	}
	reg := &mockRegistry{
		pullIndex: oci.Index{Manifests: []oci.IndexEntry{
			{Name: "pr-review", Ref: "ghcr.io/example/skills/pr-review:1.0.0"},
		}},
		pullByRef: map[string]oci.Artifact{
			"ghcr.io/example/skills/pr-review:1.0.0": artifactWithName("pr-review"),
		},
	}

	if _, err := collectionInstaller(reg, p).InstallAll(context.Background(), "intropy", "ghcr.io/example/collection:latest"); err != nil {
		t.Fatalf("InstallAll: %v", err)
	}

	manifest, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Collections) != 1 {
		t.Errorf("collection registered twice: %+v", manifest.Collections)
	}
}
