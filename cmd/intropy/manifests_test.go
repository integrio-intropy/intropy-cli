package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestManifestsCommandsAreRegistered(t *testing.T) {
	want := map[string]bool{"inspect": false, "render": false, "create": false}
	for _, cmd := range manifestsCmd.Commands() {
		if _, ok := want[cmd.Name()]; ok {
			want[cmd.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("manifests %s is not registered", name)
		}
	}
}

func TestDeployInitIsRemoved(t *testing.T) {
	for _, cmd := range deployCmd.Commands() {
		if cmd.Name() == "init" {
			t.Fatal("deploy init is still registered")
		}
	}
}

func TestManifestsCreateHasCreateOnlyFlags(t *testing.T) {
	for _, name := range []string{"env", "domain", "system", "gitops-repo", "template-version", "dry-run", "diff", "binding"} {
		if manifestsCreateCmd.Flags().Lookup(name) == nil {
			t.Errorf("manifests create must define --%s", name)
		}
	}
	for _, name := range []string{"force", "plan", "values", "set", "environments", "local"} {
		if manifestsCreateCmd.Flags().Lookup(name) != nil {
			t.Errorf("manifests create must not define --%s", name)
		}
	}
}

func TestManifestsRenderHasLocalInputFlags(t *testing.T) {
	for _, name := range []string{"env", "system", "template-version", "namespace", "image", "binding"} {
		if manifestsRenderCmd.Flags().Lookup(name) == nil {
			t.Errorf("manifests render must define --%s", name)
		}
	}
}

func TestManifestsRenderRejectsNonLocalEnvironmentBeforeOutput(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	manifestsRenderFlagValues = manifestsRenderFlags{}
	t.Cleanup(func() { manifestsRenderFlagValues = manifestsRenderFlags{} })
	rootCmd.SetArgs([]string{"manifests", "render", "--env", "prod"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a usage error")
	}
	var usage *usageError
	if !errors.As(err, &usage) {
		t.Errorf("error %q should be a usageError", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want an empty YAML stream on failure", stdout.String())
	}
}

func TestManifestsCreateRejectsLocalEnvironment(t *testing.T) {
	manifestsCreateFlagValues = manifestsCreateFlags{}
	t.Cleanup(func() { manifestsCreateFlagValues = manifestsCreateFlags{} })
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	rootCmd.SetArgs([]string{"manifests", "create", "--env", "local"})

	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "manifests render") {
		t.Fatalf("error = %v, want the render remedy", err)
	}
}
