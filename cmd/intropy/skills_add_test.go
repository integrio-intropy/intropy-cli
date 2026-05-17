package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsAddNoArgs(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsAddState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "add"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing positional arg, got nil")
	}
	// The "requires either a ref argument or --name" message starts with
	// "requires " — cmd/intropy/main.go:isUsageError matches that prefix and
	// maps to exit code 2.
	if !strings.HasPrefix(err.Error(), "requires ") {
		t.Errorf("error %q does not look like a usage error", err.Error())
	}
}

func TestSkillsAddBootstrapsSkillsJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsAddState(t, stdout, stderr)

	// Use a ref without a tag. The auto-bootstrap path runs before adder.Add,
	// and adder.Add fails the tag check before making any network call.
	rootCmd.SetArgs([]string{"skills", "add", "ghcr.io/example/skills/dummy"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing tag, got nil")
	}
	if !strings.Contains(err.Error(), "ref must include a tag") {
		t.Errorf("error %q did not surface the tag check", err.Error())
	}

	// Verify the auto-bootstrap fired before adder.Add ran.
	manifestPath := filepath.Join(tmp, "skills.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected skills.json to be auto-created at %s: %v", manifestPath, err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty on error path, got %q", stdout.String())
	}
}

// resetSkillsAddState rebinds rootCmd I/O for capture and zeroes out
// flag-backing globals so cross-test state doesn't leak.
func resetSkillsAddState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	skillsAddOpts = skillsAddFlags{}
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		skillsAddOpts = skillsAddFlags{}
	})
}
