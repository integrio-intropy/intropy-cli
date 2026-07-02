package skill

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestInstallerExtractsToCorrectPaths(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	extr := &mockExtractor{}
	inst := NewInstaller(reg, extr, p)

	entry := ManifestEntry{
		Name:    "pr-review",
		Source:  "ghcr.io/example/pr-review",
		Version: "1.0.0",
	}

	lockEntry, err := inst.Install(context.Background(), entry)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if lockEntry.Name != "pr-review" {
		t.Errorf("Name: got %q, want pr-review", lockEntry.Name)
	}
	if lockEntry.Path != ".agents/skills/pr-review" {
		t.Errorf("Path: got %q, want .agents/skills/pr-review", lockEntry.Path)
	}
	if lockEntry.Source.Ref != "ghcr.io/example/pr-review:1.0.0@sha256:abc123" {
		t.Errorf("Ref: got %q", lockEntry.Source.Ref)
	}
}

func TestInstallerSync(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	manifest := &Manifest{
		Skills: []ManifestEntry{
			{Name: "a", Source: "ghcr.io/example/a", Version: "1.0.0"},
			{Name: "b", Source: "ghcr.io/example/b", Version: "2.0.0"},
		},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("a"),
	}
	// The mock returns the same artifact for every Pull, which is fine
	// since we only verify lockfile entries, not disk content.
	extr := &mockExtractor{}
	inst := NewInstaller(reg, extr, p)

	if err := inst.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	lockfile, err := p.LoadLockfile()
	if err != nil {
		t.Fatal(err)
	}
	if len(lockfile.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(lockfile.Skills))
	}
	if lockfile.Skills[0].Name != "a" {
		t.Errorf("first skill: got %q, want a", lockfile.Skills[0].Name)
	}
	if lockfile.Skills[1].Name != "b" {
		t.Errorf("second skill: got %q, want b", lockfile.Skills[1].Name)
	}
}

func TestInstallerSyncPartialFailure(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	manifest := &Manifest{
		Skills: []ManifestEntry{
			{Name: "a", Source: "ghcr.io/example/a", Version: "1.0.0"},
			{Name: "b", Source: "ghcr.io/example/b", Version: "2.0.0"},
		},
	}
	if err := p.SaveManifest(manifest); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("a"),
		pullErr:      fmt.Errorf("pull failed"),
	}
	extr := &mockExtractor{}
	inst := NewInstaller(reg, extr, p)

	err := inst.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	// Lockfile should NOT have been written on partial failure.
	_, err = os.Stat(p.LockfilePath())
	if !os.IsNotExist(err) {
		t.Error("expected lockfile to not exist after partial failure")
	}
}

func TestInstallerAdditionalPaths(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("pr-review"),
	}
	extr := &mockExtractor{}
	inst := NewInstaller(reg, extr, p)

	entry := ManifestEntry{
		Name:                "pr-review",
		Source:              "ghcr.io/example/pr-review",
		Version:             "1.0.0",
		AdditionalBasePaths: []string{"tools", "scripts"},
	}

	lockEntry, err := inst.Install(context.Background(), entry)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(lockEntry.AdditionalPaths) != 2 {
		t.Fatalf("expected 2 additional paths, got %d", len(lockEntry.AdditionalPaths))
	}
	if lockEntry.AdditionalPaths[0] != "tools/pr-review" {
		t.Errorf("AdditionalPaths[0]: got %q, want tools/pr-review", lockEntry.AdditionalPaths[0])
	}
	if lockEntry.AdditionalPaths[1] != "scripts/pr-review" {
		t.Errorf("AdditionalPaths[1]: got %q, want scripts/pr-review", lockEntry.AdditionalPaths[1])
	}
}

func TestInstallerPullFails(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	reg := &mockRegistry{pullErr: fmt.Errorf("network down")}
	extr := &mockExtractor{}
	inst := NewInstaller(reg, extr, p)

	entry := ManifestEntry{Name: "foo", Source: "ghcr.io/example/foo", Version: "1.0.0"}
	_, err := inst.Install(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "network down") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}

func TestInstallerExtractFails(t *testing.T) {
	tmp := t.TempDir()
	p := &Project{Root: tmp}

	reg := &mockRegistry{
		pullArtifact: artifactWithName("foo"),
	}
	extr := &mockExtractor{extractErr: fmt.Errorf("disk full")}
	inst := NewInstaller(reg, extr, p)

	entry := ManifestEntry{Name: "foo", Source: "ghcr.io/example/foo", Version: "1.0.0"}
	_, err := inst.Install(context.Background(), entry)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error %q did not contain expected message", err.Error())
	}
}
