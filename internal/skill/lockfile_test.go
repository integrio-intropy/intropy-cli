package skill

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadLockfileNotFound(t *testing.T) {
	lf, err := LoadLockfile("/nonexistent/path/skills.lock.json")
	if err != nil {
		t.Fatalf("expected no error for missing lockfile, got: %v", err)
	}
	if lf.LockfileVersion != CurrentLockfileVersion {
		t.Errorf("expected version %d, got %d", CurrentLockfileVersion, lf.LockfileVersion)
	}
	if len(lf.Skills) != 0 {
		t.Errorf("expected empty skills, got %d", len(lf.Skills))
	}
}

func TestLockfileRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills.lock.json")

	original := &Lockfile{
		Skills: []LockEntry{
			{
				Name: "pr-review",
				Path: ".agents/skills/pr-review",
				Source: LockSource{
					Registry:   "ghcr.io",
					Repository: "example/pr-review",
					Tag:        "1.0.0",
					Digest:     "sha256:abc123",
					Ref:        "ghcr.io/example/pr-review:1.0.0@sha256:abc123",
				},
				InstalledAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	if err := SaveLockfile(path, original); err != nil {
		t.Fatalf("SaveLockfile: %v", err)
	}

	loaded, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}

	if len(loaded.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded.Skills))
	}
	entry := loaded.Skills[0]
	if entry.Name != "pr-review" {
		t.Errorf("name: got %q, want pr-review", entry.Name)
	}
	if entry.Source.Tag != "1.0.0" {
		t.Errorf("tag: got %q, want 1.0.0", entry.Source.Tag)
	}
	if loaded.LockfileVersion != CurrentLockfileVersion {
		t.Errorf("version: got %d, want %d", loaded.LockfileVersion, CurrentLockfileVersion)
	}
}

func TestLoadLockfileBadVersion(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills.lock.json")
	content := `{"lockfileVersion": 999, "skills": []}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadLockfile(path)
	if err == nil {
		t.Fatal("expected error for bad lockfile version")
	}
}
