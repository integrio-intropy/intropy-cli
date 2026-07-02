package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestCollectionAddMissingName(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionAddState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "collection", "add", "--ref", "ghcr.io/example/index:latest"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --name")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("--name is required")) {
		t.Errorf("error %q did not mention --name", err.Error())
	}
}

func TestCollectionAddMissingRef(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionAddState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "collection", "add", "--name", "example"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --ref")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("--ref is required")) {
		t.Errorf("error %q did not mention --ref", err.Error())
	}
}

func TestCollectionAddDuplicate(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionAddState(t, stdout, stderr)

	// First add should work (but fails at network since no registry)
	// Instead, create skills.json directly with a collection.
	writeFileT(t, tmp+"/skills.json", `{"skills":[],"collections":[{"name":"example","ref":"ghcr.io/example/index:v1"}]}`+"\n")

	rootCmd.SetArgs([]string{"skills", "collection", "add", "--name", "example", "--ref", "ghcr.io/example/index:v2"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for duplicate")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("already registered")) {
		t.Errorf("error %q did not mention already registered", err.Error())
	}
}

func TestCollectionAddInvalidRef(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSkillsCollectionAddState(t, stdout, stderr)

	rootCmd.SetArgs([]string{"skills", "collection", "add", "--name", "example", "--ref", "not-a-valid-ref"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
	var ue *usageError
	if errors.As(err, &ue) {
		t.Error("invalid ref is a runtime error, not usage")
	}
}

func resetSkillsCollectionAddState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	skillsCollectionAddOpts = skillsCollectionAddFlags{output: "plain"}
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		skillsCollectionAddOpts = skillsCollectionAddFlags{output: "plain"}
	})
}
