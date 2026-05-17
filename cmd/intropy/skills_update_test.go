package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestSkillsUpdateMissingName(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsUpdateState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "update"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q is not a usageError", err.Error())
	}
}

func TestSkillsUpdateNameAndAll(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsUpdateState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "update", "foo", "--all"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for name + --all")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q is not a usageError", err.Error())
	}
}

func TestSkillsUpdateNoSkills(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	// Bootstrap empty skills.json
	writeFileT(t, tmp+"/skills.json", `{"skills":[]}`+"\n")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsUpdateState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "update", "--all"})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("No skills installed.")) {
		t.Errorf("expected 'No skills installed.' on stderr, got %q", stderr.String())
	}
}

func resetSkillsUpdateState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	skillsUpdateOpts = skillsUpdateFlags{}
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		skillsUpdateOpts = skillsUpdateFlags{}
	})
}
