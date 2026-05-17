package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsPublishMissingFlags(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsPublishState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "publish"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing required flags, got nil")
	}
	// cobra emits "required flag(s) ..." which isUsageError matches to exit 2.
	if !strings.HasPrefix(err.Error(), "required flag(s)") {
		t.Errorf("error %q does not look like a required-flag usage error", err.Error())
	}
}

func TestSkillsPublishInvalidPath(t *testing.T) {
	tmp := t.TempDir()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsPublishState(t, stdout, stderr)

	missing := filepath.Join(tmp, "does-not-exist")
	rootCmd.SetArgs([]string{
		"skills", "publish",
		"--path", missing,
		"--ref", "localhost:5000/test/skill",
		"--tag", "1.0.0",
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent --path, got nil")
	}
	if !strings.Contains(err.Error(), "pack:") {
		t.Errorf("error %q did not surface the pack failure", err.Error())
	}
}

func resetSkillsPublishState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	skillsPublishOpts = skillsPublishFlags{}
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		skillsPublishOpts = skillsPublishFlags{}
	})
}
