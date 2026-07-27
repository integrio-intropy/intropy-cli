package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/gitops/gitopstest"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
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
	coord := gitops.Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	origin := gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:    coord.String(),
		Image:         image,
		Environments:  []string{"dev", "prod"},
		OverlayImages: "images:\n  - name: " + image + "\n    newTag: latest\n",
	})

	source, _ := newSourceClone(t)

	// Point the user config at the GitOps origin.
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("INTROPY_GITOPS_REPO", "")
	gittest.WriteFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: "+origin+"\n")

	return runFixture{gitopsOrigin: origin, sourceDir: source, cacheRoot: t.TempDir(), image: image}
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

	root := gitops.CheckoutDir(f.cacheRoot, f.gitopsOrigin)
	g := git.Client{Runner: command.ExecRunner{}, Dir: root}
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
	if res.SyncPolicy != gitops.SyncAuto {
		t.Errorf("SyncPolicy = %q, want %q", res.SyncPolicy, gitops.SyncAuto)
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
	gittest.WriteFile(t, filepath.Join(t.TempDir(), "unused"), "")

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	gittest.WriteFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: /nonexistent/repo\n")

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
	root := gitops.CheckoutDir(f.cacheRoot, f.gitopsOrigin)
	overlay := filepath.Join(root, filepath.FromSlash("domains/orders/order-flow/order-extractor/overlays/dev"))
	k := kustomize.Client{Runner: command.ExecRunner{}}
	if err := k.SetImage(ctx, overlay, f.image, testDigest); err != nil {
		t.Fatal(err)
	}
	if err := k.SetAnnotation(ctx, overlay, kustomize.AnnotationSourceCommit, currentHEAD(t, f.sourceDir)); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, root, "add", "-A")
	gittest.Run(t, root, "commit", "--quiet", "-m", "pin")
	gittest.Run(t, root, "push", "--quiet", "origin", "main")

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
	sha, err := git.Client{Runner: command.ExecRunner{}, Dir: dir}.HEAD(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// The apply path, end to end through Run: the change must reach the GitOps
// origin, and the reported revision must be what landed there.
func TestRunAppliesAndPushes(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.OutputFormat = OutputJSON

	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	var res Result
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !res.Applied {
		t.Error("Applied should be true once the change is pushed")
	}
	if len(res.Revision) != 40 {
		t.Errorf("Revision = %q, want a full sha", res.Revision)
	}
	if got := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main"); got != res.Revision {
		t.Errorf("origin/main = %q, want the reported revision %q", got, res.Revision)
	}
	if !strings.Contains(gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%s", "main"), "deploy(order-extractor)") {
		t.Error("the deployment commit is not on the origin")
	}
}

// Re-running after a successful apply must be a no-op: the digest is already
// pinned, so there is nothing to commit and no empty commit is created.
func TestRunTwiceCreatesOneCommit(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatal(err)
	}
	after := gittest.Run(t, f.gitopsOrigin, "rev-list", "--count", "main")

	stdout.Reset()
	stderr.Reset()
	if err := Run(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatal(err)
	}
	if got := gittest.Run(t, f.gitopsOrigin, "rev-list", "--count", "main"); got != after {
		t.Errorf("commit count went from %s to %s; the second run should be a no-op", after, got)
	}
	if !strings.Contains(stdout.String(), "already at") {
		t.Errorf("the second run should report a no-op:\n%s", stdout.String())
	}
}

// A manual-sync environment is gated in ArgoCD, so deploy commits but must not
// wait for a sync that will never start on its own.
func TestRunManualSyncStopsAfterCommit(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "prod"

	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "syncs manually") {
		t.Errorf("stdout should say the environment syncs manually:\n%s", out)
	}
	if !strings.Contains(out, "deploy sync") {
		t.Errorf("stdout should name the follow-up command:\n%s", out)
	}
	// Committed all the same.
	if !strings.Contains(gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%s", "main"), "deploy(order-extractor)") {
		t.Error("a manual-sync environment should still get the commit")
	}
}

// --plan must write nothing, even now that the apply path exists.
func TestRunPlanOnlyStillWritesNothing(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	before := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	if got := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main"); got != before {
		t.Errorf("origin/main moved to %q; --plan must not write", got)
	}
	root := gitops.CheckoutDir(f.cacheRoot, f.gitopsOrigin)
	if dirty := gittest.Run(t, root, "status", "--porcelain"); dirty != "" {
		t.Errorf("--plan must leave the checkout clean, got:\n%s", dirty)
	}
}

// decodeResult parses a JSON Result, failing the test on malformed output.
func decodeResult(t *testing.T, b []byte) Result {
	t.Helper()
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, b)
	}
	return res
}
