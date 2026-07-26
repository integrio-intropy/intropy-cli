package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// resetDeployState restores the flag-backing global between tests. These tests
// execute the real rootCmd in-process, so without this a flag set by one test
// leaks into the next.
func resetDeployState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	deployFlagValues = deployFlags{output: "plain"}
	t.Cleanup(func() { deployFlagValues = deployFlags{output: "plain"} })
}

func runDeploy(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetDeployState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"deploy"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestDeployRequiresComponent(t *testing.T) {
	_, _, err := runDeploy(t, "--env", "dev")
	if err == nil {
		t.Fatal("expected an error when no component is given")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestDeployRequiresEnv(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor")
	if err == nil {
		t.Fatal("expected an error when --env is missing")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestDeployRejectsUnknownOutputFormat(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor", "--env", "dev", "--output", "yaml")
	if err == nil {
		t.Fatal("expected an error for an unsupported output format")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q should be a usageError", err)
	}
	if !strings.Contains(err.Error(), "json") {
		t.Errorf("error %q should list the supported formats", err)
	}
}

func TestDeployAcceptsBothOutputFormats(t *testing.T) {
	// These fail later, on configuration — the point is that the flag itself is
	// accepted and does not produce a usage error.
	for _, format := range []string{"plain", "json"} {
		_, _, err := runDeploy(t, "order-extractor", "--env", "dev", "--output", format)
		var ue *usageError
		if errors.As(err, &ue) {
			t.Errorf("--output %s should be accepted, got usage error %q", format, err)
		}
	}
}

// The command must not define its own PersistentPreRunE: Cobra runs only the
// closest one, so it would shadow the root's and silently break -C/--directory
// for this command alone.
func TestDeployDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if deployCmd.PersistentPreRunE != nil || deployCmd.PersistentPreRun != nil {
		t.Error("deploy must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
}

func TestDeployIsRegistered(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "deploy" {
			return
		}
	}
	t.Error("deploy is not registered on the root command")
}

// Completion must never fail a shell or print a diagnostic, even with no
// configuration and no cached checkout.
func TestDeployCompletionsAreSilentWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTROPY_GITOPS_REPO", "")

	if got, _ := completeDeployComponents(deployCmd, nil, ""); len(got) != 0 {
		t.Errorf("component completion = %v, want none", got)
	}
	if got, _ := completeDeployEnvironments(deployCmd, nil, ""); len(got) != 0 {
		t.Errorf("environment completion = %v, want none", got)
	}
	// A component already supplied means there is nothing left to complete.
	if got, _ := completeDeployComponents(deployCmd, []string{"order-extractor"}, ""); len(got) != 0 {
		t.Errorf("completion after an argument = %v, want none", got)
	}
}
