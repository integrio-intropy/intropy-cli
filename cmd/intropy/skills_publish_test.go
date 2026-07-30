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
	// Cobra's missing-required-flag error maps to exit code 2 via isCobraUsageError.
	if !isCobraUsageError(err) {
		t.Errorf("error %q is not a recognized usage error", err.Error())
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
		"--version", "1.0.0",
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent --path, got nil")
	}
	if !strings.Contains(err.Error(), "pack:") {
		t.Errorf("error %q did not surface the pack failure", err.Error())
	}
}

func TestSkillsPublishVersionFlag(t *testing.T) {
	reset := func(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
		t.Helper()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		resetSkillsPublishState(t, stdout, stderr)
		for _, name := range []string{"version", "tag"} {
			f := skillsPublishCmd.Flags().Lookup(name)
			f.Changed = false
			t.Cleanup(func() { f.Changed = false })
		}
		return stdout, stderr
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	t.Run("missing --version is a usage error", func(t *testing.T) {
		reset(t)
		rootCmd.SetArgs([]string{"skills", "publish", "--path", missing, "--ref", "localhost:5000/test/skill"})
		err := rootCmd.Execute()
		if err == nil || !strings.Contains(err.Error(), `"version"`) {
			t.Fatalf("err = %v, want required-flag error for version", err)
		}
		if exitCode(err) != 2 {
			t.Errorf("exitCode = %d, want 2", exitCode(err))
		}
	})

	t.Run("deprecated --tag warns and still works", func(t *testing.T) {
		_, stderr := reset(t)
		rootCmd.SetArgs([]string{"skills", "publish", "--path", missing, "--ref", "localhost:5000/test/skill", "--tag", "1.0.0"})
		err := rootCmd.Execute()
		// pack failure on the missing path proves the alias satisfied the requirement
		if err == nil || !strings.Contains(err.Error(), "pack:") {
			t.Fatalf("err = %v, want pack failure (alias accepted)", err)
		}
		if !strings.Contains(stderr.String(), "--tag is deprecated") {
			t.Errorf("expected deprecation warning, got: %q", stderr.String())
		}
	})

	t.Run("--tag and --version with different values are a usage error", func(t *testing.T) {
		reset(t)
		rootCmd.SetArgs([]string{"skills", "publish", "--path", missing, "--ref", "localhost:5000/test/skill", "--tag", "1.0.0", "--version", "2.0.0"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "cannot combine") {
			t.Errorf("unexpected error: %v", err)
		}
		if exitCode(err) != 2 {
			t.Errorf("exitCode = %d, want 2", exitCode(err))
		}
	})

	t.Run("deprecated --tag is hidden from help", func(t *testing.T) {
		f := skillsPublishCmd.Flags().Lookup("tag")
		if f == nil {
			t.Fatal("--tag alias missing")
		}
		if !f.Hidden {
			t.Error("deprecated --tag alias must be hidden from help")
		}
	})
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
