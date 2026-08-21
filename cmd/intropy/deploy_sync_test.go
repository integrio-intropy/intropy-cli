package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func resetSyncState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	syncFlagValues = syncFlags{output: "plain"}
	t.Cleanup(func() { syncFlagValues = syncFlags{output: "plain"} })
}

func runSync(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetSyncState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"deploy", "sync"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestSyncRequiresComponent(t *testing.T) {
	_, _, err := runSync(t, "--env", "prod")
	if err == nil {
		t.Fatal("expected an error when no component is given")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestSyncRequiresEnv(t *testing.T) {
	_, _, err := runSync(t, "order-extractor")
	if err == nil {
		t.Fatal("expected an error when --env is missing")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestSyncRejectsUnknownOutputFormat(t *testing.T) {
	_, _, err := runSync(t, "order-extractor", "--env", "prod", "--output", "yaml")
	if err == nil {
		t.Fatal("expected an error for an unsupported output format")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q should be a usageError", err)
	}
}

// The reviewed-revision guard is the reason this command can be trusted with
// production, so the flag has to exist.
func TestSyncHasRevisionFlag(t *testing.T) {
	if deploySyncCmd.Flags().Lookup("revision") == nil {
		t.Error("--revision is documented but not registered")
	}
}

// A sync renders nothing, so it must not offer flags that describe a plan.
func TestSyncHasNoPlanFlag(t *testing.T) {
	if deploySyncCmd.Flags().Lookup("plan") != nil {
		t.Error("sync must not define --plan: it writes nothing to git and renders nothing")
	}
}

func TestSyncDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if deploySyncCmd.PersistentPreRunE != nil || deploySyncCmd.PersistentPreRun != nil {
		t.Error("sync must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
}

// Under deploy, which is the command deploy and promote already tell people to
// run when an environment syncs manually.
func TestSyncIsRegisteredUnderDeploy(t *testing.T) {
	for _, c := range deployCmd.Commands() {
		if c.Name() == "sync" {
			return
		}
	}
	t.Error("sync is not registered under deploy")
}

// The exact string deploy and promote print when an environment syncs manually.
// If this drifts from the registered command, the CLI is telling people to run
// something that does not work — which is what it did before this command
// existed.
func TestTheAdvertisedSyncCommandResolves(t *testing.T) {
	advertised := []string{"deploy", "sync", "order-extractor", "--env", "prod"}

	cmd, _, err := rootCmd.Find(advertised)
	if err != nil {
		t.Fatalf("the advertised command does not resolve: %v", err)
	}
	if cmd != deploySyncCmd {
		t.Errorf("resolved to %q, want the sync command", cmd.CommandPath())
	}
}

func TestSyncSubcommandWinsOverTheComponentArgument(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetSyncState(t, stdout, stderr)
	rootCmd.SetArgs([]string{"deploy", "sync", "order-extractor"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a usage error for the missing --env")
	}
	// deploy would reject the *second* positional argument instead.
	if !strings.Contains(err.Error(), "--env") {
		t.Errorf("error %q suggests deploy ran instead of sync", err)
	}
}
