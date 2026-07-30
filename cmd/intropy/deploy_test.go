package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
	rootCmd.SetArgs(append([]string{"deploy", "pin"}, args...))
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
	for _, cmd := range []*cobra.Command{deployCmd, deployPinCmd} {
		if cmd.PersistentPreRunE != nil || cmd.PersistentPreRun != nil {
			t.Errorf("%s must not define PersistentPreRunE; it would shadow the root's and break -C", cmd.Name())
		}
	}
}

// deploy is a pure parent — the runnable form is 'deploy pin' — so a
// component called diff, pin or sync is never shadowed by a subcommand.
func TestDeployIsNotRunnable(t *testing.T) {
	if deployCmd.RunE != nil || deployCmd.Run != nil {
		t.Error("deploy must not be runnable; the digest-pinning command is 'deploy pin'")
	}
	if deployPinCmd.RunE == nil {
		t.Error("deploy pin must be the runnable digest-pinning command")
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

	if got, _ := completeDeployComponents(deployPinCmd, nil, ""); len(got) != 0 {
		t.Errorf("component completion = %v, want none", got)
	}
	if got, _ := completeDeployEnvironments(deployPinCmd, nil, ""); len(got) != 0 {
		t.Errorf("environment completion = %v, want none", got)
	}
	// A component already supplied means there is nothing left to complete.
	if got, _ := completeDeployComponents(deployPinCmd, []string{"order-extractor"}, ""); len(got) != 0 {
		t.Errorf("completion after an argument = %v, want none", got)
	}
}

func TestDeployRejectsThreeArguments(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor", "1.4.2", "extra", "--env", "staging")
	if err == nil {
		t.Fatal("expected an error for a third positional argument")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

// --allow-dirty advertises a working-tree check, and a release deploy reads no
// working tree. Ignoring the flag would be worse than refusing it.
func TestDeployRejectsAllowDirtyWithAVersion(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor", "1.4.2", "--env", "staging", "--allow-dirty")
	if err == nil {
		t.Fatal("expected an error for --allow-dirty with a version")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("error should be a usage error, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--allow-dirty") {
		t.Errorf("error should name the flag: %v", err)
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// A release records the digests it was created from, so there is nothing for
// --watch to wait on. Refusing beats silently accepting a flag that does
// nothing.
func TestDeployRejectsWatchWithAVersion(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor", "1.4.2", "--env", "staging", "--watch")
	if err == nil {
		t.Fatal("expected an error for --watch with a version")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("error should be a usage error, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--watch") {
		t.Errorf("error should name the flag: %v", err)
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// The -w shorthand must parse, like kubectl's.
func TestDeployAcceptsWatchShorthand(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor", "--env", "dev", "-w", "--gitops-repo", "/nonexistent/repo")
	var ue *usageError
	if errors.As(err, &ue) {
		t.Errorf("-w must not be a usage error: %v", err)
	}
}

// The version is accepted as a second positional argument: this must fail for
// want of configuration, not for want of valid arguments.
func TestDeployAcceptsAVersionArgument(t *testing.T) {
	_, _, err := runDeploy(t, "order-extractor", "1.4.2", "--env", "staging", "--gitops-repo", "/nonexistent/repo")
	var ue *usageError
	if errors.As(err, &ue) {
		t.Errorf("a version argument must not be a usage error: %v", err)
	}
}

func TestVersionArg(t *testing.T) {
	if got := versionArg([]string{"order-extractor"}); got != "" {
		t.Errorf("versionArg with one arg = %q, want empty", got)
	}
	if got := versionArg([]string{"order-extractor", "1.4.2"}); got != "1.4.2" {
		t.Errorf("versionArg = %q, want 1.4.2", got)
	}
}

// The --argocd-server flag is documented in the README, so it must exist.
func TestDeployHasArgocdServerFlag(t *testing.T) {
	if deployPinCmd.Flags().Lookup("argocd-server") == nil {
		t.Error("--argocd-server is documented but not registered")
	}
}
