package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func resetInitState(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	resetRootIO(t, stdout, stderr)
	initFlagValues = initFlags{output: "plain"}
	t.Cleanup(func() { initFlagValues = initFlags{output: "plain"} })
}

func runDeployInit(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetInitState(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"deploy", "init"}, args...))
	return stdout, stderr, rootCmd.Execute()
}

func TestDeployInitIsRegisteredUnderDeploy(t *testing.T) {
	for _, c := range deployCmd.Commands() {
		if c.Name() == "init" {
			return
		}
	}
	t.Fatal("init is not registered under deploy")
}

// deploy is not runnable, so a component called init is reachable via
// 'deploy pin init' like any other — there are no reserved names to warn
// about, and the parent's help lists init as a subcommand.
func TestDeployHelpNamesInitAsSubcommand(t *testing.T) {
	if !strings.Contains(deployCmd.Long, "init") {
		t.Error("deploy's long help must list init among its subcommands")
	}
}

func TestDeployInitRejectsUnknownOutputFormat(t *testing.T) {
	_, _, err := runDeployInit(t, "--output", "yaml")
	if err == nil {
		t.Fatal("expected an error for an unsupported output format")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q should be a usageError", err)
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestDeployInitRejectsMalformedSet(t *testing.T) {
	_, _, err := runDeployInit(t, "--set", "novalue")
	if err == nil {
		t.Fatal("expected an error for a --set without =")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %q should be a usageError", err)
	}
}

// This writes a branch for review and syncs nothing, so there is no ArgoCD
// interaction to configure and no source working tree to check.
func TestDeployInitHasNoArgocdOrDirtyFlags(t *testing.T) {
	for _, name := range []string{"argocd-server", "no-wait", "timeout", "allow-dirty", "revision"} {
		if deployInitCmd.Flags().Lookup(name) != nil {
			t.Errorf("init must not define --%s", name)
		}
	}
}

func TestDeployInitDocumentedFlagsExist(t *testing.T) {
	for _, name := range []string{
		"domain", "system", "environments",
		"template-version", "values", "set", "no-input", "plan", "force",
		"gitops-repo", "output",
	} {
		if deployInitCmd.Flags().Lookup(name) == nil {
			t.Errorf("init must define --%s", name)
		}
	}
}

func TestDeployInitVersionIsTheTemplateRelease(t *testing.T) {
	f := deployInitCmd.Flags().Lookup("template-version")
	if f == nil {
		t.Fatal("init must define --template-version")
	}
	if !strings.Contains(f.Usage, "template") {
		t.Errorf("--template-version usage = %q, want it to say it is the template release", f.Usage)
	}
}

// Reaching --template-version on the subcommand must set the template
// release, not be swallowed by the root command's version flag.
func TestDeployInitVersionFlagBinds(t *testing.T) {
	resetInitState(t, &bytes.Buffer{}, &bytes.Buffer{})
	if err := deployInitCmd.Flags().Set("template-version", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if initFlagValues.templateVersion != "v1.2.3" {
		t.Errorf("templateVersion = %q, want v1.2.3", initFlagValues.templateVersion)
	}
}

// Everywhere else --env/-e names the single target environment a deploy acts
// on; init selects which overlays to scaffold, so it takes --environments
// (plural, repeatable) with no shorthand. A value with a comma must survive:
// StringSlice would split it, StringArray does not.
func TestDeployInitEnvironmentFlagShape(t *testing.T) {
	if deployInitCmd.Flags().Lookup("env") != nil {
		t.Error("init must not define --env: that name means a single target environment in the other deploy commands")
	}
	envs := deployInitCmd.Flags().Lookup("environments")
	if envs == nil {
		t.Fatal("init must define --environments")
	}
	if envs.Shorthand != "" {
		t.Errorf("--environments shorthand = %q, want none (-e would collide with the single-env commands)", envs.Shorthand)
	}

	values := deployInitCmd.Flags().Lookup("values")
	if values == nil {
		t.Fatal("init must define --values")
	}
	if values.Value.Type() != "stringArray" {
		t.Errorf("--values type = %q, want stringArray (no comma splitting, matching int create)", values.Value.Type())
	}
	envsType := deployInitCmd.Flags().Lookup("environments").Value.Type()
	if envsType != "stringArray" {
		t.Errorf("--environments type = %q, want stringArray (no comma splitting)", envsType)
	}
}

// --domain places the system rather than narrowing a search, which is the
// opposite of every other deploy subcommand. The help has to warn about it.
func TestDeployInitDomainHelpFlagsTheDifferentMeaning(t *testing.T) {
	if !strings.Contains(deployInitCmd.Long, "--domain places the system") {
		t.Error("init's long help must explain that --domain is a destination, not a filter")
	}
}

// A tree full of placeholders on the default branch would be picked up by the
// ApplicationSet immediately, so the help must promise a branch.
func TestDeployInitHelpPromisesABranch(t *testing.T) {
	for _, want := range []string{"deploy-init/", "not pushed to the default branch"} {
		if strings.Contains(deployInitCmd.Long, want) {
			return
		}
	}
	if !strings.Contains(deployInitCmd.Long, "deploy-init/<domain>-<system>") {
		t.Error("init's long help must say which branch it pushes")
	}
}

func TestDeployInitDoesNotShadowRootPersistentPreRun(t *testing.T) {
	if deployInitCmd.PersistentPreRunE != nil || deployInitCmd.PersistentPreRun != nil {
		t.Error("init must not define PersistentPreRunE; it would shadow the root's and break -C")
	}
}

func TestDeployInitAcceptsNoPositionalArgs(t *testing.T) {
	if deployInitCmd.Args == nil {
		t.Fatal("init must declare an Args validator")
	}
	if err := deployInitCmd.Args(deployInitCmd, nil); err != nil {
		t.Errorf("no arguments must be allowed (the whole system): %v", err)
	}
	if err := deployInitCmd.Args(deployInitCmd, []string{"a", "b"}); err != nil {
		t.Errorf("several components must be allowed: %v", err)
	}
}
