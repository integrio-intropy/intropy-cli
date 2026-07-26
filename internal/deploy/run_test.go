package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// runFixture builds everything Run needs: a GitOps repository with one
// onboarded component, a source repository whose HEAD is pushed, a config file
// pointing at the GitOps origin, and a stubbed digest resolver.
type runFixture struct {
	gitopsOrigin string
	sourceDir    string
	cacheRoot    string
	image        string
}

func newRunFixture(t *testing.T) runFixture {
	t.Helper()
	requireKustomize(t)

	image := "harbor.intropy.io/integrations/order-extractor"
	coord := Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	compRel := filepath.FromSlash(coord.RelPath())

	gitops := t.TempDir()
	runGit(t, gitops, "init", "--quiet", "--initial-branch=main")
	runGit(t, gitops, "config", "user.email", "test@example.com")
	runGit(t, gitops, "config", "user.name", "Test")
	runGit(t, gitops, "config", "commit.gpgsign", "false")
	// The origin is a non-bare checkout, so allow pushes to its current branch.
	runGit(t, gitops, "config", "receive.denyCurrentBranch", "ignore")
	writeFile(t, filepath.Join(gitops, DeployFileName), validDeployYAML)
	writeFile(t, filepath.Join(gitops, compRel, ComponentFileName),
		"schemaVersion: 1\nname: order-extractor\nsourcePaths: [component/]\nimages:\n  - name: "+image+"\nenvironments: [dev, prod]\n")
	writeFile(t, filepath.Join(gitops, compRel, "base", "deployment.yaml"),
		strings.ReplaceAll(baseDeployment, "IMAGE", image))
	writeFile(t, filepath.Join(gitops, compRel, "base", "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n")
	writeFile(t, filepath.Join(gitops, compRel, OverlaysDirName, "dev", "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\nimages:\n  - name: "+image+"\n    newTag: latest\n")
	runGit(t, gitops, "add", ".")
	runGit(t, gitops, "commit", "--quiet", "-m", "onboard")

	source, _ := newSourceClone(t)

	// Point the user config at the GitOps origin.
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("INTROPY_GITOPS_REPO", "")
	writeFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: "+gitops+"\n")

	return runFixture{gitopsOrigin: gitops, sourceDir: source, cacheRoot: t.TempDir(), image: image}
}

// stubDigest replaces the production resolver for the duration of a test.
func stubDigest(t *testing.T, digest string) {
	t.Helper()
	original := NewResolver
	NewResolver = func(string) (Resolver, error) {
		return stubResolver{desc: registry.Descriptor{Digest: digest}}, nil
	}
	t.Cleanup(func() { NewResolver = original })
}

func (f runFixture) options(stdout, stderr *bytes.Buffer) Options {
	return Options{
		Component:   "order-extractor",
		Environment: "dev",
		SourceDir:   f.sourceDir,
		CacheRoot:   f.cacheRoot,
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

func TestRunPlanOnly(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true

	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	if !strings.Contains(out, testDigest) {
		t.Errorf("stdout should show the new digest:\n%s", out)
	}
	if !strings.Contains(out, "orders/order-flow/order-extractor") {
		t.Errorf("stdout should identify the component:\n%s", out)
	}
	if !strings.Contains(out, "@@") {
		t.Errorf("stdout should contain the diff:\n%s", out)
	}
	// Diagnostics belong on stderr so `--plan | ...` stays parseable.
	if !strings.Contains(stderr.String(), "nothing was committed") {
		t.Errorf("stderr should say nothing was committed:\n%s", stderr.String())
	}
	if strings.Contains(out, "refreshing") {
		t.Errorf("progress output leaked onto stdout:\n%s", out)
	}
}

// Nothing in this step commits, so the shared checkout must be left clean
// whichever path Run takes.
func TestRunLeavesTheCheckoutClean(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	root := worktreeDir(f.cacheRoot, f.gitopsOrigin)
	g := Git{Runner: ExecRunner{}, Dir: root}
	dirty, err := g.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("Run must not leave the cached checkout dirty, got %v", dirty)
	}
}

func TestRunJSONOutput(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true
	opts.OutputFormat = OutputJSON

	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	// Must be parseable on its own — no diff, no progress lines mixed in.
	var res Result
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if res.Component != "order-extractor" || res.Domain != "orders" || res.System != "order-flow" {
		t.Errorf("coordinate wrong: %+v", res)
	}
	if res.AppName != "orders-order-flow-order-extractor-dev" {
		t.Errorf("AppName = %q", res.AppName)
	}
	if res.OverlayPath != "domains/orders/order-flow/order-extractor/overlays/dev" {
		t.Errorf("OverlayPath = %q", res.OverlayPath)
	}
	if res.SyncPolicy != SyncAuto {
		t.Errorf("SyncPolicy = %q, want %q", res.SyncPolicy, SyncAuto)
	}
	if !res.Changed || res.Applied {
		t.Errorf("Changed/Applied = %v/%v, want true/false", res.Changed, res.Applied)
	}
	if len(res.Pins) != 1 || res.Pins[0].Digest != testDigest || res.Pins[0].Previous != ":latest" {
		t.Errorf("Pins = %+v", res.Pins)
	}
}

func TestRunUnknownEnvironment(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "qa"

	err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected an error for an unknown environment")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Errorf("error %q should list the defined environments", err)
	}
}

func TestRunUnknownComponent(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Component = "nope"

	if err := Run(context.Background(), opts); err == nil {
		t.Fatal("expected an error for an unknown component")
	}
}

// With no configured repository the error must name every way to supply one,
// and must arrive before anything touches the network.
func TestRunWithoutConfiguredRepo(t *testing.T) {
	requireKustomize(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTROPY_GITOPS_REPO", "")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		Component:   "order-extractor",
		Environment: "dev",
		CacheRoot:   t.TempDir(),
		Stdout:      &stdout,
		Stderr:      &stderr,
	})
	if err == nil {
		t.Fatal("expected an error when no GitOps repository is configured")
	}
	for _, want := range []string{"--gitops-repo", "INTROPY_GITOPS_REPO", "gitopsRepo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// The --gitops-repo flag must win over the config file.
func TestRunFlagOverridesConfiguredRepo(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	writeFile(t, filepath.Join(t.TempDir(), "unused"), "")

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	writeFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: /nonexistent/repo\n")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true
	opts.GitopsRepo = f.gitopsOrigin

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("--gitops-repo should override the config file: %v", err)
	}
}

func TestRunAlreadyPinnedReportsNoOp(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	ctx := context.Background()

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true
	if err := Run(ctx, opts); err != nil {
		t.Fatal(err)
	}

	// Commit the pin into the origin so the next run finds it already applied.
	root := worktreeDir(f.cacheRoot, f.gitopsOrigin)
	overlay := filepath.Join(root, filepath.FromSlash("domains/orders/order-flow/order-extractor/overlays/dev"))
	k := Kustomize{Runner: ExecRunner{}}
	if err := k.SetImage(ctx, overlay, f.image, testDigest); err != nil {
		t.Fatal(err)
	}
	if err := k.SetAnnotation(ctx, overlay, AnnotationSourceCommit, currentHEAD(t, f.sourceDir)); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "--quiet", "-m", "pin")
	runGit(t, root, "push", "--quiet", "origin", "main")

	stdout.Reset()
	stderr.Reset()
	opts = f.options(&stdout, &stderr)
	opts.PlanOnly = true
	if err := Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "already at") {
		t.Errorf("a re-run should report a no-op:\n%s", stdout.String())
	}
}

func currentHEAD(t *testing.T, dir string) string {
	t.Helper()
	sha, err := Git{Runner: ExecRunner{}, Dir: dir}.HEAD(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sha
}
