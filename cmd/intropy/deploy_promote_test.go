package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func resetPromoteState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	promoteFlagValues = promoteFlags{output: "plain"}
	t.Cleanup(func() { promoteFlagValues = promoteFlags{output: "plain"} })
}

func runPromote(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetPromoteState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"deploy", "promote"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestPromoteRequiresComponent(t *testing.T) {
	_, _, err := runPromote(t, "--from", "staging", "--to", "prod")
	if err == nil {
		t.Fatal("expected an error when no component is given")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
}

func TestPromoteRequiresFromAndTo(t *testing.T) {
	for _, args := range [][]string{
		{"order-extractor"},
		{"order-extractor", "--from", "staging"},
		{"order-extractor", "--to", "prod"},
	} {
		_, _, err := runPromote(t, args...)
		if err == nil {
			t.Fatalf("%v: expected an error for missing environments", args)
		}
		if code := exitCode(err); code != 2 {
			t.Errorf("%v: exit code = %d, want 2", args, code)
		}
	}
}

func TestPromoteRejectsUnknownOutputFormat(t *testing.T) {
	_, _, err := runPromote(t, "order-extractor", "--from", "staging", "--to", "prod", "--output", "yaml")
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

func TestPromoteRejectsTheSameEnvironment(t *testing.T) {
	_, _, err := runPromote(t, "order-extractor", "--from", "prod", "--to", "prod")
	if err == nil {
		t.Fatal("expected an error for --from == --to")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
	if !strings.Contains(err.Error(), "both prod") {
		t.Errorf("error %q should name the environment", err)
	}
}

// A promotion reads no source repository and no registry, so it must not offer
// the flags that only make sense when something is resolved. Their presence
// would advertise a check that never runs.
func TestPromoteHasNoResolutionFlags(t *testing.T) {
	for _, name := range []string{"allow-dirty", "env"} {
		if deployPromoteCmd.Flags().Lookup(name) != nil {
			t.Errorf("promote must not define --%s: it resolves nothing and takes --from/--to", name)
		}
	}
}

func TestPromoteDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if deployPromoteCmd.PersistentPreRunE != nil || deployPromoteCmd.PersistentPreRun != nil {
		t.Error("promote must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
}

// Under deploy, not the root: the message deploy already prints for a
// manual-sync environment names `intropy deploy sync`, and promote's own close
// names the same command.
func TestPromoteIsRegisteredUnderDeploy(t *testing.T) {
	for _, c := range deployCmd.Commands() {
		if c.Name() == "promote" {
			return
		}
	}
	t.Error("promote is not registered under deploy")
}

func TestPromoteCompletionsAreSilentWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTROPY_GITOPS_REPO", "")

	if got, _ := completeDeployComponents(deployPromoteCmd, nil, ""); len(got) != 0 {
		t.Errorf("component completion = %v, want none", got)
	}
	if got, _ := completeDeployEnvironments(deployPromoteCmd, nil, ""); len(got) != 0 {
		t.Errorf("environment completion = %v, want none", got)
	}
}

// The completions read the invoked command's own --gitops-repo. Reading deploy's
// global would make them silently consult the wrong repository under promote.
func TestPromoteCompletionsReadItsOwnGitopsRepoFlag(t *testing.T) {
	if err := deployPromoteCmd.Flags().Set("gitops-repo", "/promote/repo"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deployPromoteCmd.Flags().Set("gitops-repo", "") })

	if got := gitopsRepoFlag(deployPromoteCmd); got != "/promote/repo" {
		t.Errorf("gitopsRepoFlag = %q, want promote's own value", got)
	}
}

// The subcommand name wins over the component argument, which is what makes
// `deploy promote x` reach this command rather than deploying a component called
// promote.
func TestPromoteSubcommandWinsOverTheComponentArgument(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetPromoteState(t, stdout, stderr)
	rootCmd.SetArgs([]string{"deploy", "promote", "order-extractor"})

	// Reaching promote means --from/--to are missing; reaching deploy would
	// instead complain about --env.
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("error %q suggests deploy ran instead of promote", err)
	}
}
