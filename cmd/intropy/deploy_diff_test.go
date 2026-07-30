package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func resetDiffState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	diffFlagValues = diffFlags{output: "plain"}
	t.Cleanup(func() { diffFlagValues = diffFlags{output: "plain"} })
}

func runDiff(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetDiffState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"deploy", "diff"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestDiffRequiresComponent(t *testing.T) {
	_, _, err := runDiff(t, "--env", "prod")
	if err == nil {
		t.Fatal("expected an error when no component is given")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestDiffRequiresEnv(t *testing.T) {
	_, _, err := runDiff(t, "order-extractor")
	if err == nil {
		t.Fatal("expected an error when --env is missing")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestDiffRejectsUnknownOutputFormat(t *testing.T) {
	_, _, err := runDiff(t, "order-extractor", "--env", "prod", "--output", "yaml")
	if err == nil {
		t.Fatal("expected an error for an unsupported output format")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q should be a usageError", err)
	}
}

// A diff reads and prints. Flags that describe waiting for a convergence it never
// triggers would be an offer it cannot honour.
func TestDiffHasNoWaitingFlags(t *testing.T) {
	for _, name := range []string{"no-wait", "timeout", "plan", "revision"} {
		if deployDiffCmd.Flags().Lookup(name) != nil {
			t.Errorf("diff must not define --%s: it renders two commits and prints the difference", name)
		}
	}
}

func TestDiffDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if deployDiffCmd.PersistentPreRunE != nil || deployDiffCmd.PersistentPreRun != nil {
		t.Error("diff must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
}

func TestDiffIsRegisteredUnderDeploy(t *testing.T) {
	for _, c := range deployCmd.Commands() {
		if c.Name() == "diff" {
			return
		}
	}
	t.Error("diff is not registered under deploy")
}

// The exact string deploy prints when an environment syncs manually. If this
// drifts from the registered command, the CLI is telling an approver to run
// something that does not work.
func TestTheAdvertisedDiffCommandResolves(t *testing.T) {
	advertised := []string{"deploy", "diff", "order-extractor", "--env", "prod"}

	cmd, _, err := rootCmd.Find(advertised)
	if err != nil {
		t.Fatalf("the advertised command does not resolve: %v", err)
	}
	if cmd != deployDiffCmd {
		t.Errorf("resolved to %q, want the diff command", cmd.CommandPath())
	}
}

func TestDiffSubcommandWinsOverTheComponentArgument(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetDiffState(t, stdout, stderr)
	rootCmd.SetArgs([]string{"deploy", "diff", "order-extractor"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a usage error for the missing --env")
	}
	// deploy would reject the *second* positional argument instead.
	if !strings.Contains(err.Error(), "--env") {
		t.Errorf("error %q suggests deploy ran instead of diff", err)
	}
}
