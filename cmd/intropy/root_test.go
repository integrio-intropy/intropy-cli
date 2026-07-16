package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func resetChangeDirFlag(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		changeDirFlag = ""
	})
}

func TestRootChangeDirectory(t *testing.T) {
	projectDir := t.TempDir()
	writeFileT(t, filepath.Join(projectDir, "skills.json"), `{"skills":[{"name":"pr-review","source":"ghcr.io/example/skills/pr-review","version":"1.0.0"}]}`+"\n")
	writeFileT(t, filepath.Join(projectDir, "skills.lock.json"), `{
  "lockfileVersion": 1,
  "generatedAt": "2025-01-01T00:00:00Z",
  "skills": [
    {
      "name": "pr-review",
      "path": ".agents/skills/pr-review",
      "source": {
        "registry": "ghcr.io",
        "repository": "example/skills/pr-review",
        "tag": "1.0.0",
        "digest": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
        "ref": "ghcr.io/example/skills/pr-review:1.0.0@sha256:abc"
      },
      "installedAt": "2025-01-01T00:00:00Z"
    }
  ]
}
`)

	t.Chdir(t.TempDir())
	resetChangeDirFlag(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"-C", projectDir, "skills", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "pr-review") {
		t.Errorf("expected skills from %s, got:\n%s", projectDir, stdout.String())
	}
}

func TestRootChangeDirectoryNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	resetChangeDirFlag(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"-C", filepath.Join(t.TempDir(), "nonexistent"), "skills", "list"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	if !strings.Contains(err.Error(), "cannot change to directory") {
		t.Errorf("expected 'cannot change to directory' error, got %v", err)
	}
}
