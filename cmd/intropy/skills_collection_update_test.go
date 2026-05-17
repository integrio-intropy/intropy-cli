package main

import (
	"bytes"
	"testing"
)

func TestCollectionUpdateUnknownAlias(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionUpdateState(t, stdout, stderr)

	writeFileT(t, tmp+"/skills.json", `{"skills":[],"collections":[]}`+"\n")

	rootCmd.SetArgs([]string{"skills", "collection", "update", "nonexistent"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown alias")
	}
}

func TestCollectionUpdateNoArgs(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionUpdateState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "collection", "update"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing arg")
	}
}

func resetSkillsCollectionUpdateState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	skillsCollectionUpdateOpts = skillsCollectionUpdateFlags{output: "plain"}
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		skillsCollectionUpdateOpts = skillsCollectionUpdateFlags{output: "plain"}
	})
}
