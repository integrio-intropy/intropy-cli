package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func resetStatusState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	statusFlagValues = statusFlags{output: "plain"}
	t.Cleanup(func() { statusFlagValues = statusFlags{output: "plain"} })
}

func runStatus(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetStatusState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"deploy", "status"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestStatusRequiresComponent(t *testing.T) {
	_, _, err := runStatus(t)
	if err == nil {
		t.Fatal("expected an error when no component is given")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestStatusRejectsUnknownOutputFormat(t *testing.T) {
	_, _, err := runStatus(t, "order-extractor", "--output", "yaml")
	if err == nil {
		t.Fatal("expected an error for an unsupported output format")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q should be a usageError", err)
	}
}

// Status is every environment at once. An --env flag would suggest it can be
// narrowed to one, and the question about one environment — what a sync would
// apply — is deploy diff.
func TestStatusHasNoEnvironmentOrWaitingFlags(t *testing.T) {
	for _, name := range []string{"env", "no-wait", "timeout", "plan", "revision", "allow-dirty"} {
		if deployStatusCmd.Flags().Lookup(name) != nil {
			t.Errorf("status must not define --%s: it reports every environment and waits for nothing", name)
		}
	}
}

func TestStatusDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if deployStatusCmd.PersistentPreRunE != nil || deployStatusCmd.PersistentPreRun != nil {
		t.Error("status must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
}

func TestStatusIsRegisteredUnderDeploy(t *testing.T) {
	for _, c := range deployCmd.Commands() {
		if c.Name() == "status" {
			return
		}
	}
	t.Error("status is not registered under deploy")
}

func TestStatusCompletionsAreSilentWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTROPY_GITOPS_REPO", "")

	if got, _ := completeDeployComponents(deployStatusCmd, nil, ""); len(got) != 0 {
		t.Errorf("component completion = %v, want none", got)
	}
}

func TestStatusSubcommandWinsOverTheComponentArgument(t *testing.T) {
	args := []string{"deploy", "status", "order-extractor"}

	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("deploy status does not resolve: %v", err)
	}
	if cmd != deployStatusCmd {
		t.Errorf("resolved to %q, want the status command", cmd.CommandPath())
	}
}

// deploy is not runnable, so a component called status deploys via
// 'deploy pin status' like any other. The parent's help lists status as a
// subcommand.
func TestDeployHelpNamesStatusAsSubcommand(t *testing.T) {
	if !strings.Contains(deployCmd.Long, "status") {
		t.Error("deploy's Long should name status among its subcommands")
	}
}
