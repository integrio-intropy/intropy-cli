package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackSkill(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "pr-review")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write SKILL.md with valid frontmatter
	skillMD := `---
name: pr-review
version: "1.0.0"
description: A skill for reviewing pull requests
---

# PR Review Skill
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a regular file
	if err := os.WriteFile(filepath.Join(skillDir, "prompt.md"), []byte("# Prompt\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a subdirectory
	subDir := filepath.Join(skillDir, "templates")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "review.md"), []byte("template"), 0644); err != nil {
		t.Fatal(err)
	}

	artifact, err := Pack(skillDir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if artifact.Config.Name != "pr-review" {
		t.Errorf("Config.Name: got %q, want pr-review", artifact.Config.Name)
	}
	if artifact.Config.Version != "1.0.0" {
		t.Errorf("Config.Version: got %q, want 1.0.0", artifact.Config.Version)
	}

	// Verify tarball contents
	content, err := io.ReadAll(artifact.Content)
	if err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var foundFiles []string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		foundFiles = append(foundFiles, hdr.Name)
	}

	expected := []string{
		"pr-review/",
		"pr-review/SKILL.md",
		"pr-review/prompt.md",
		"pr-review/templates/",
		"pr-review/templates/review.md",
	}
	if len(foundFiles) != len(expected) {
		t.Errorf("expected %d entries, got %d: %v", len(expected), len(foundFiles), foundFiles)
	}
}

func TestPackMissingSKILLmd(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "bad-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := Pack(skillDir)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
}

func TestPackNotADirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Pack(file)
	if err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestPackRejectsSymlink(t *testing.T) {
	// Skip on Windows where symlinks require special permissions
	if os.Getenv("CI") == "" && os.Getenv("GITHUB_ACTIONS") == "" {
		// Only test on Unix-like systems in CI
		return
	}

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "bad-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	skillMD := "---\nname: bad-skill\n---\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(skillDir, "evil")); err != nil {
		t.Skip("cannot create symlinks on this system")
	}

	_, err := Pack(skillDir)
	if err == nil {
		t.Fatal("expected error for symlink")
	}
}
