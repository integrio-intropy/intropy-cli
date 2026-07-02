package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectionPublishMissingSpecFile(t *testing.T) {
	tmp := t.TempDir()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionPublishState(t, stdout, stderr)

	missing := filepath.Join(tmp, "missing.yaml")
	rootCmd.SetArgs([]string{
		"skills", "collection", "publish", missing,
		"localhost:5000/test/collection:1.0.0",
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing spec file, got nil")
	}
	if !strings.Contains(err.Error(), "read spec") {
		t.Errorf("error %q did not surface the spec read failure", err.Error())
	}
}

func TestCollectionPublishMalformedSpec(t *testing.T) {
	tmp := t.TempDir()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionPublishState(t, stdout, stderr)

	// Spec without `name`, which loadCollectionSpec rejects.
	specPath := filepath.Join(tmp, "spec.yaml")
	if err := os.WriteFile(specPath, []byte("description: x\nskills:\n  - ref: localhost:5000/x/y:1.0\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	rootCmd.SetArgs([]string{
		"skills", "collection", "publish", specPath,
		"localhost:5000/test/collection:1.0.0",
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for spec missing name, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error %q did not surface the missing-name validation", err.Error())
	}
}

func resetSkillsCollectionPublishState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	skillsCollectionPublishOpts = skillsCollectionPublishFlags{}
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		skillsCollectionPublishOpts = skillsCollectionPublishFlags{}
	})
}
