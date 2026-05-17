package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsListProjectNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error on empty tempdir, got %v", err)
	}
	if !strings.Contains(stdout.String(), "No skills installed.") {
		t.Errorf("expected 'No skills installed.' on stdout, got %q", stdout.String())
	}
}

func TestSkillsListEmptyLockfile(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeFileT(t, filepath.Join(tmp, "skills.json"), `{"skills":[]}`+"\n")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "No skills installed.") {
		t.Errorf("expected 'No skills installed.' on stdout, got %q", stdout.String())
	}
}

func TestSkillsListPopulated(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeFileT(t, filepath.Join(tmp, "skills.json"), `{"skills":[{"name":"pr-review","source":"ghcr.io/example/skills/pr-review","version":"1.0.0"}]}`+"\n")
	writeFileT(t, filepath.Join(tmp, "skills.lock.json"), `{
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

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"NAME", "pr-review", "1.0.0", "sha256:abcdef012345", ".agents/skills/pr-review"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
}

func resetRootIO(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
