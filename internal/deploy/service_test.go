//go:build integration

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
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

const testCommit = "def456abc789def456abc789def456abc789def4"

// newSourceClone creates an origin repository and a clone of it, returning the
// clone. Source checks reason about the remote, so a clone with a real origin
// is the minimum honest fixture.
func newSourceClone(t *testing.T) (clone, origin string) {
	t.Helper()
	origin = gittest.NewRepo(t, "main")
	clone = filepath.Join(t.TempDir(), "src")
	if err := git.Clone(context.Background(), command.ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	return clone, origin
}

// stubResolver returns a fixed descriptor for every image.
type stubResolver struct {
	desc registry.Descriptor
	err  error
}

func (s stubResolver) Resolve(context.Context, string) (registry.Descriptor, error) {
	return s.desc, s.err
}

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
	return newRunFixtureWithImage(t, "harbor.intropy.io/integrations/order-extractor")
}

// newRunFixtureWithImage is newRunFixture with the image repository chosen, so
// a release test can host it on an in-memory registry.
//
// The staging overlay exists but is left unpinned: gitopstest applies
// OverlayImages to the first environment only, which is exactly the shape the
// promotesFrom tests want — dev pinned, staging not.
func newRunFixtureWithImage(t *testing.T, image string) runFixture {
	t.Helper()
	requireKustomize(t)

	coord := gitops.Coordinate{Domain: "orders", System: "order-flow", Component: "order-extractor"}
	origin := gitopstest.NewRepo(t, gitopstest.Component{
		Coordinate:    coord.String(),
		Image:         image,
		Environments:  []string{"dev", "staging", "prod"},
		OverlayImages: "images:\n  - name: " + image + "\n    newTag: latest\n",
	})

	src, _ := newSourceClone(t)

	// Point the user config at the GitOps origin.
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("INTROPY_GITOPS_REPO", "")
	gittest.WriteFile(t, filepath.Join(cfgHome, "intropy", "config.yaml"), "gitopsRepo: "+origin+"\n")

	return runFixture{gitopsOrigin: origin, sourceDir: src, cacheRoot: t.TempDir(), image: image}
}

// stubDigest replaces the production resolver for the duration of a test.
func stubDigest(t *testing.T, digest string) {
	t.Helper()
	original := source.NewResolver
	source.NewResolver = func(string) (source.Resolver, error) {
		return stubResolver{desc: registry.Descriptor{Digest: digest}}, nil
	}
	t.Cleanup(func() { source.NewResolver = original })
}

// lateResolver stands in for a pipeline that has not finished: it reports the
// tag missing a fixed number of times before resolving, so a watch test does
// not need a real registry or a long wait.
type lateResolver struct {
	misses int
	desc   registry.Descriptor
}

func (r *lateResolver) Resolve(context.Context, string) (registry.Descriptor, error) {
	if r.misses > 0 {
		r.misses--
		return registry.Descriptor{}, registry.ErrNotFound
	}
	return r.desc, nil
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

// With --watch, running the deploy before CI finished is not a failure: the
// command polls until the tag appears and proceeds from there.
func TestRunWatchWaitsForTheImage(t *testing.T) {
	f := newRunFixture(t)

	resolver := &lateResolver{misses: 1, desc: registry.Descriptor{Digest: testDigest}}
	original := source.NewResolver
	source.NewResolver = func(string) (source.Resolver, error) { return resolver, nil }
	t.Cleanup(func() { source.NewResolver = original })

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Watch = true
	opts.NoWait = true

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run() with watch: %v\nstderr: %s", err, stderr.String())
	}
	if resolver.misses != 0 {
		t.Error("the resolver should have been asked again after the miss")
	}
	if !strings.Contains(stderr.String(), "waiting for") {
		t.Errorf("stderr should report the wait:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "committed") {
		t.Errorf("the deploy should have proceeded once the image appeared:\n%s", stdout.String())
	}
}

// Without --watch, an unpublished tag fails on the spot — the flag is what
// buys the wait.
func TestRunWithoutWatchFailsImmediately(t *testing.T) {
	f := newRunFixture(t)

	resolver := &lateResolver{misses: 5, desc: registry.Descriptor{Digest: testDigest}}
	original := source.NewResolver
	source.NewResolver = func(string) (source.Resolver, error) { return resolver, nil }
	t.Cleanup(func() { source.NewResolver = original })

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.NoWait = true

	err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run() without watch should fail on an unpublished tag")
	}
	if !strings.Contains(err.Error(), "pipeline has not published") {
		t.Errorf("error %q should explain the pipeline", err)
	}
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
	// Both halves of the gate: read the rendered change, then apply it.
	if !strings.Contains(out, "deploy diff") || !strings.Contains(out, "deploy sync") {
		t.Errorf("stdout should name both follow-up commands:\n%s", out)
	}
	// The sha --revision wants. Without it the approver has to find the revision
	// they are approving for themselves, and the guard goes unused.
	pushed := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")
	if !strings.Contains(out, "--revision "+pushed) {
		t.Errorf("stdout should offer the pushed revision %s:\n%s", pushed, out)
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

// pinOverlay commits a digest pin plus the source-commit annotation into one
// environment's overlay on the GitOps origin. It stands in for an earlier
// deployment to that environment.
//
// It works through its own clone rather than the deploy cache, so it never
// races the cache's lock.
func (f runFixture) pinOverlay(t *testing.T, env, digest, commit string) {
	t.Helper()
	f.pinOverlayRelease(t, env, digest, commit, "")
}

// pinTag leaves an overlay pinned to a tag rather than a digest — the state most
// existing overlays are in, and one a promotion has to refuse.
func (f runFixture) pinTag(t *testing.T, env, tag string) {
	t.Helper()
	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")

	rel := filepath.Join("domains", "orders", "order-flow", "order-extractor", "overlays", env, "kustomization.yaml")
	gittest.WriteFile(t, filepath.Join(clone, rel),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\n"+
			"images:\n  - name: "+f.image+"\n    newTag: "+tag+"\n")

	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "--quiet", "-m", "deploy("+env+"): tag")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
}

// requireNothingWritten asserts that the GitOps branch did not move. Every
// refusal in this package must leave the repository exactly as it found it, and
// a command that refuses after committing would be worse than one that never
// refused.
func (f runFixture) requireNothingWritten(t *testing.T) {
	t.Helper()
	log := gittest.Run(t, f.gitopsOrigin, "log", "--format=%s", "main")
	for line := range strings.SplitSeq(log, "\n") {
		if strings.HasPrefix(line, "deploy(order-extractor):") {
			t.Errorf("a refusal must write nothing, but the origin carries %q", line)
		}
	}
}

// pinOverlayRelease is pinOverlay for an overlay deployed from a release, which
// carries the version annotation as well. Promotion reads that annotation, so a
// fixture without it cannot exercise the version-to-version path.
func (f runFixture) pinOverlayRelease(t *testing.T, env, digest, commit, version string) {
	t.Helper()
	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")

	annotations := "commonAnnotations:\n  " + kustomize.AnnotationSourceCommit + ": " + commit + "\n"
	if version != "" {
		annotations += "  " + kustomize.AnnotationRelease + ": " + version + "\n"
	}

	rel := filepath.Join("domains", "orders", "order-flow", "order-extractor", "overlays", env, "kustomization.yaml")
	gittest.WriteFile(t, filepath.Join(clone, rel),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\n"+
			"images:\n  - name: "+f.image+"\n    digest: "+digest+"\n"+annotations)

	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "--quiet", "-m", "deploy("+env+"): pin")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
}

// The reassurance the staging step exists for.
func TestRunStagingReportsTheUpstreamMatch(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	f.pinOverlay(t, "dev", testDigest, testReleaseCommit)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "staging"
	opts.PlanOnly = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "dev already runs this digest") {
		t.Errorf("the plan should say the digest matches dev:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "tested bits") {
		t.Errorf("the plan should say these are the tested bits:\n%s", stdout.String())
	}
}

func TestRunStagingReportsAnUpstreamDifference(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	f.pinOverlay(t, "dev", upstreamDigest, testReleaseCommit)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "staging"
	opts.PlanOnly = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "runs a different digest") {
		t.Errorf("the plan should report the mismatch:\n%s", stdout.String())
	}
}

// The comparison is informational: a mismatch reports, it does not refuse.
func TestRunUpstreamMismatchStillDeploys(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	f.pinOverlay(t, "dev", upstreamDigest, testReleaseCommit)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "staging"
	opts.NoWait = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("a digest dev never ran must still deploy: %v", err)
	}

	trailers := gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%(trailers:only=true)", "main")
	if !strings.Contains(trailers, TrailerEnvironment+": staging") {
		t.Errorf("the staging deploy should have landed:\n%s", trailers)
	}
}

func TestRunJSONCarriesUpstreams(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	f.pinOverlay(t, "dev", testDigest, testReleaseCommit)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "staging"
	opts.PlanOnly = true
	opts.OutputFormat = OutputJSON
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	res := decodeResult(t, stdout.Bytes())
	if len(res.Upstreams) != 1 {
		t.Fatalf("want one upstream in the JSON result, got %+v", res.Upstreams)
	}
	if res.Upstreams[0].Environment != "dev" || res.Upstreams[0].Status != UpstreamMatch {
		t.Errorf("upstream = %+v, want dev/%s", res.Upstreams[0], UpstreamMatch)
	}
	if res.Release != "" {
		t.Errorf("Release = %q, want empty for a commit deploy", res.Release)
	}
}

// Steps 1 and 2 must be untouched: dev promotes from nothing.
func TestRunDevHasNoUpstreamLine(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.PlanOnly = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	for _, unwanted := range []string{"already runs", "nothing to compare", "different digest"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Errorf("dev has no promotesFrom, so %q should not appear:\n%s", unwanted, stdout.String())
		}
	}
}

// A no-op re-run still answers the question that prompted the command.
func TestRunNoOpStillReportsTheUpstream(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)
	// The annotation must match the commit being deployed too, or the plan is
	// provenance-only rather than empty.
	head := currentHEAD(t, f.sourceDir)
	f.pinOverlay(t, "dev", testDigest, head)
	f.pinOverlay(t, "staging", testDigest, head)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "staging"
	opts.PlanOnly = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	if !strings.Contains(out, "already at") {
		t.Fatalf("staging is already at that digest:\n%s", out)
	}
	if !strings.Contains(out, "dev already runs this digest") {
		t.Errorf("a no-op should still report the upstream comparison:\n%s", out)
	}
}
