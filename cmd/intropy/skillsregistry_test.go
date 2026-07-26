package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/skill"
	"github.com/integrio-intropy/intropy-cli/internal/skill/oci"
)

// fakeRegistry records what a command pushed and reports every ref as
// absent, so publish takes its happy path.
type fakeRegistry struct {
	pushedRef string
	pushedCfg oci.Config
}

func (f *fakeRegistry) Pull(context.Context, string) (oci.Artifact, error) {
	return oci.Artifact{}, oci.ErrNotFound
}

func (f *fakeRegistry) PullIndex(context.Context, string) (oci.Index, error) {
	return oci.Index{}, oci.ErrNotFound
}

func (f *fakeRegistry) Resolve(context.Context, string) (oci.Descriptor, error) {
	return oci.Descriptor{}, oci.ErrNotFound
}

func (f *fakeRegistry) Push(_ context.Context, ref string, art oci.Artifact) (oci.Descriptor, error) {
	f.pushedRef = ref
	f.pushedCfg = art.Config
	return oci.Descriptor{Digest: "sha256:deadbeef", Size: 1234}, nil
}

func (f *fakeRegistry) PushIndex(context.Context, string, oci.Index) (oci.Descriptor, error) {
	return oci.Descriptor{}, nil
}

// stubSkillRegistry swaps the registry factory for the duration of a test.
func stubSkillRegistry(t *testing.T, reg skill.Registry) {
	t.Helper()
	original := newSkillRegistry
	newSkillRegistry = func() (skill.Registry, error) { return reg, nil }
	t.Cleanup(func() { newSkillRegistry = original })
}

func TestSkillsPublishUsesInjectedRegistry(t *testing.T) {
	skillDir := filepath.Join(t.TempDir(), "pr-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := `---
name: pr-review
description: A skill for reviewing pull requests
---

# PR Review Skill
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeRegistry{}
	stubSkillRegistry(t, fake)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsPublishState(t, stdout, stderr)

	rootCmd.SetArgs([]string{
		"skills", "publish",
		"--path", skillDir,
		"--ref", "harbor.intropy.io/skills/pr-review",
		"--tag", "1.2.0",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("skills publish: %v", err)
	}

	if fake.pushedRef != "harbor.intropy.io/skills/pr-review:1.2.0" {
		t.Errorf("pushed ref = %q; want the repo and tag joined", fake.pushedRef)
	}
	if fake.pushedCfg.Name != "pr-review" {
		t.Errorf("pushed config name = %q; want %q", fake.pushedCfg.Name, "pr-review")
	}
	if !strings.Contains(stdout.String(), "sha256:deadbeef") {
		t.Errorf("output %q does not report the pushed digest", stdout.String())
	}
}

// TestNewSkillRegistryDefaultIsUsable guards the un-stubbed factory: the
// commands are only as good as the client it hands them. That the User-Agent
// reaches the wire is covered in internal/registry.
func TestNewSkillRegistryDefaultIsUsable(t *testing.T) {
	client, err := newSkillRegistry()
	if err != nil {
		t.Fatalf("newSkillRegistry: %v", err)
	}
	if client == nil {
		t.Fatal("newSkillRegistry returned a nil registry")
	}
}
