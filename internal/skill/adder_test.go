package skill

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/intropy/intropy-cli/internal/skill/oci"
)

type mockRegistry struct {
	pullArtifact oci.Artifact
	pullErr      error
}

func (m *mockRegistry) Pull(ctx context.Context, ref string) (oci.Artifact, error) {
	if m.pullErr != nil {
		return oci.Artifact{}, m.pullErr
	}
	return m.pullArtifact, nil
}

func (m *mockRegistry) PullIndex(ctx context.Context, ref string) (oci.Index, error) {
	return oci.Index{}, fmt.Errorf("not implemented")
}

func (m *mockRegistry) Resolve(ctx context.Context, ref string) (oci.Descriptor, error) {
	return oci.Descriptor{}, fmt.Errorf("not implemented")
}

func (m *mockRegistry) Push(ctx context.Context, ref string, art oci.Artifact) (oci.Descriptor, error) {
	return oci.Descriptor{}, fmt.Errorf("not implemented")
}

func (m *mockRegistry) PushIndex(ctx context.Context, ref string, idx oci.Index) (oci.Descriptor, error) {
	return oci.Descriptor{}, fmt.Errorf("not implemented")
}

type mockExtractor struct {
	extractErr error
}

func (m *mockExtractor) Extract(ctx context.Context, layer io.Reader, dests []string) error {
	return m.extractErr
}

func artifactWithName(name string) oci.Artifact {
	return oci.Artifact{
		Config: oci.Config{
			SchemaVersion: oci.SupportedSchemaVersion,
			Name:          name,
		},
		Content: io.NopCloser(strings.NewReader("content")),
		Digest:  "sha256:abc123",
		Tag:     "1.0.0",
	}
}

func TestAdderMissingTag(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	installer := NewInstaller(&mockRegistry{}, &mockExtractor{}, p)
	add := NewAdder(&mockRegistry{}, installer, p)
	_, err := add.Add(context.Background(), "ghcr.io/example/skill", AddOptions{})
	if err == nil {
		t.Fatal("expected error for missing tag")
	}
	if !strings.Contains(err.Error(), "ref must include a tag") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}

func TestAdderDuplicate(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	manifest := &Manifest{
		Skills: []ManifestEntry{
			{Name: "pr-review", Source: "ghcr.io/example/pr-review", Version: "1.0.0"},
		},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	installer := NewInstaller(reg, &mockExtractor{}, p)
	add := NewAdder(reg, installer, p)
	_, err := add.Add(context.Background(), "ghcr.io/example/pr-review:1.0.0", AddOptions{})
	if err == nil {
		t.Fatal("expected error for duplicate")
	}
	if !strings.Contains(err.Error(), "already in the manifest") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}

func TestAdderSuccess(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	if err := p.SaveManifest(&Manifest{Skills: []ManifestEntry{}}); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	installer := NewInstaller(reg, &mockExtractor{}, p)
	add := NewAdder(reg, installer, p)

	entry, err := add.Add(context.Background(), "ghcr.io/example/pr-review:1.0.0", AddOptions{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if entry.Name != "pr-review" {
		t.Errorf("Name: got %q, want pr-review", entry.Name)
	}

	manifest, err := p.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0].Name != "pr-review" {
		t.Errorf("manifest: got %+v", manifest.Skills)
	}

	lockfile, err := p.LoadLockfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(lockfile.Skills) != 1 || lockfile.Skills[0].Name != "pr-review" {
		t.Errorf("lockfile: got %+v", lockfile.Skills)
	}
}

func TestAdderPullFails(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	if err := p.SaveManifest(&Manifest{Skills: []ManifestEntry{}}); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{pullErr: fmt.Errorf("network error")}
	installer := NewInstaller(reg, &mockExtractor{}, p)
	add := NewAdder(reg, installer, p)

	_, err := add.Add(context.Background(), "ghcr.io/example/skill:1.0.0", AddOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}

func TestAdderInstallFails(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	if err := p.SaveManifest(&Manifest{Skills: []ManifestEntry{}}); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	extr := &mockExtractor{extractErr: fmt.Errorf("extract failed")}
	installer := NewInstaller(reg, extr, p)
	add := NewAdder(reg, installer, p)

	_, err := add.Add(context.Background(), "ghcr.io/example/pr-review:1.0.0", AddOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "extract failed") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}

func TestUpsertLockEntry(t *testing.T) {
	entries := []LockEntry{
		{Name: "a", Path: ".agents/skills/a"},
		{Name: "b", Path: ".agents/skills/b"},
	}

	// Update existing
	updated := upsertLockEntry(entries, LockEntry{Name: "a", Path: ".agents/skills/a-new"})
	if len(updated) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(updated))
	}
	if updated[0].Path != ".agents/skills/a-new" {
		t.Errorf("path not updated: got %q", updated[0].Path)
	}

	// Append new
	appended := upsertLockEntry(updated, LockEntry{Name: "c", Path: ".agents/skills/c"})
	if len(appended) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(appended))
	}
}
