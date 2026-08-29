package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/config"
)

// clearConfigEnv empties every environment variable that participates in
// config resolution so a test sees only the file it wrote. Clearing works
// because resolution treats an empty value as unset; CI may export the
// borrowed ARGOCD_SERVER.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvGitopsRepo, "")
	t.Setenv(config.EnvTemplateRepo, "")
	t.Setenv(config.EnvArgocdServer, "")
	t.Setenv(config.EnvArgocdAuthToken, "")
}

// runContext executes the context command with the given args and captures
// stdout and stderr. Flag values are package-level, so they are reset
// between runs.
func runContext(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, out, errOut)
	t.Cleanup(func() {
		contextListOpts.output = "plain"
		contextShowOpts.output = "plain"
	})
	rootCmd.SetArgs(append([]string{"context"}, args...))
	err = rootCmd.Execute()
	return out.String(), errOut.String(), err
}

const twoContextConfig = `currentContext: acme
contexts:
  integrio:
    organization: integrio
  acme:
    organization: acme
    gitopsRepo: git@gitlab.com:acme/gitops.git
`

func TestContextListPlain(t *testing.T) {
	withConfig(t, twoContextConfig)
	clearConfigEnv(t)
	stdout, _, err := runContext(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	// Sorted, never file order: acme precedes integrio even though integrio
	// comes first in the file.
	want := "acme (active)\nintegrio\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestContextListJSON(t *testing.T) {
	withConfig(t, twoContextConfig)
	clearConfigEnv(t)
	stdout, _, err := runContext(t, "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"contexts": [`, `"acme"`, `"integrio"`, `"active": "acme"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q should contain %q", stdout, want)
		}
	}
}

func TestContextListEmptyState(t *testing.T) {
	withConfig(t, "gitopsRepo: git@gitlab.com:acme/gitops.git\n")
	clearConfigEnv(t)
	stdout, stderr, err := runContext(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no contexts configured") {
		t.Errorf("stderr %q should name the empty state", stderr)
	}
	if !strings.Contains(stderr, "config.yaml") {
		t.Errorf("stderr %q should point at the config file", stderr)
	}
}

func TestContextListEmptyStateJSONStaysSilent(t *testing.T) {
	withConfig(t, "")
	clearConfigEnv(t)
	stdout, stderr, err := runContext(t, "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"contexts": []`) {
		t.Errorf("stdout %q should be an empty document", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want silent json", stderr)
	}
}

func TestContextListRejectsUnknownFormat(t *testing.T) {
	withConfig(t, twoContextConfig)
	clearConfigEnv(t)
	_, _, err := runContext(t, "list", "--output", "yaml")
	if err == nil {
		t.Fatal("--output yaml should fail")
	}
	if !strings.Contains(err.Error(), "plain") || !strings.Contains(err.Error(), "json") {
		t.Errorf("error %q should list the allowed formats", err)
	}
}

func TestContextShowPlain(t *testing.T) {
	withConfig(t, twoContextConfig)
	clearConfigEnv(t)
	stdout, _, err := runContext(t, "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "organization: acme (context)") {
		t.Errorf("stdout %q should show the context-sourced organization", stdout)
	}
	if !strings.Contains(stdout, "gitopsRepo: git@gitlab.com:acme/gitops.git (context)") {
		t.Errorf("stdout %q should show the context-sourced gitopsRepo", stdout)
	}
}

func TestContextShowEnvBeatsContext(t *testing.T) {
	withConfig(t, twoContextConfig)
	clearConfigEnv(t)
	t.Setenv(config.EnvGitopsRepo, "git@gitlab.com:env/gitops.git")
	stdout, _, err := runContext(t, "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "gitopsRepo: git@gitlab.com:env/gitops.git (env)") {
		t.Errorf("stdout %q should annotate the env-sourced value", stdout)
	}
}

func TestContextShowFlatKeysAreFileSourced(t *testing.T) {
	withConfig(t, "organization: integrio\ngitopsRepo: git@gitlab.com:integrio/gitops.git\n")
	clearConfigEnv(t)
	stdout, _, err := runContext(t, "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "organization: integrio (file)") {
		t.Errorf("stdout %q should annotate file-sourced values", stdout)
	}
}

func TestContextShowEmptyState(t *testing.T) {
	withConfig(t, "")
	clearConfigEnv(t)
	stdout, stderr, err := runContext(t, "show")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no settings configured") {
		t.Errorf("stderr %q should name the empty state", stderr)
	}
}

func TestContextShowStaleCurrentContextFails(t *testing.T) {
	withConfig(t, "currentContext: ghost\ncontexts:\n  acme: {}\n")
	clearConfigEnv(t)
	_, _, err := runContext(t, "show")
	if err == nil {
		t.Fatal("a dangling currentContext should fail at load")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "acme") {
		t.Errorf("error %q should name the bad pointer and the valid contexts", err)
	}
}

func TestContextShowJSON(t *testing.T) {
	withConfig(t, twoContextConfig)
	clearConfigEnv(t)
	stdout, _, err := runContext(t, "show", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"organization"`, `"value": "acme"`, `"source": "context"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q should contain %q", stdout, want)
		}
	}
}
