package deploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// stagingDigest is what staging runs in these fixtures, distinct from testDigest
// so a promotion that accidentally resolved something would be visible.
const stagingDigest = "sha256:ad22d6f2ecbc03e79f0123456789abcdef0123456789abcdef0123456789abcd"

func (f runFixture) promoteOptions(from, to string, stdout, stderr *bytes.Buffer) PromoteOptions {
	return PromoteOptions{
		Component: "order-extractor",
		From:      from,
		To:        to,
		CacheRoot: f.cacheRoot,
		Stdout:    stdout,
		Stderr:    stderr,
	}
}

// overlayOf reads one environment's overlay from a fresh clone of the origin.
func (f runFixture) overlayOf(t *testing.T, env string) *kustomize.Kustomization {
	t.Helper()
	dir := filepath.Join(f.cloneOrigin(t), filepath.FromSlash("domains/orders/order-flow/order-extractor/overlays/"+env))
	k, _, err := kustomize.ReadKustomization(dir)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// failIfRegistryUsed proves a promotion consults no registry at all. It is the
// load-bearing assertion of the whole command: if a digest can be resolved, it
// can be resolved to something other than what the source environment runs.
func failIfRegistryUsed(t *testing.T) {
	t.Helper()
	original := source.NewResolver
	source.NewResolver = func(string) (source.Resolver, error) {
		t.Error("a promotion must not resolve anything: it copies the digests the source environment already pins")
		return nil, errors.New("resolver must not be used")
	}
	t.Cleanup(func() { source.NewResolver = original })
}

// healthySource makes ArgoCD report the source application Synced and Healthy at
// the revision that last changed its overlay, which is what requireSourceHealthy
// demands.
func (f runFixture) healthySource(t *testing.T, env string) *stubArgoClient {
	t.Helper()
	revision := gittest.Run(t, f.cloneOrigin(t), "log", "-1", "--format=%H", "--",
		"domains/orders/order-flow/order-extractor/overlays/"+env)

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-" + env: healthyApp(revision),
		},
	}
	stubArgo(t, stub)
	return stub
}

// The command in one test: staging's digest reaches prod byte for byte, with no
// registry involved and the version carried across.
func TestPromoteCopiesTheSourceDigest(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "staging", stagingDigest, testCommit, "1.4.2")
	failIfRegistryUsed(t)
	f.healthySource(t, "staging")

	var stdout, stderr bytes.Buffer
	opts := f.promoteOptions("staging", "prod", &stdout, &stderr)
	opts.OutputFormat = OutputJSON

	if err := Promote(context.Background(), opts); err != nil {
		t.Fatalf("promote: %v\nstderr: %s", err, stderr.String())
	}

	res := decodeResult(t, stdout.Bytes())
	if res.Environment != "prod" {
		t.Errorf("Environment = %q, want prod", res.Environment)
	}
	if len(res.Pins) != 1 || res.Pins[0].Digest != stagingDigest {
		t.Fatalf("pins = %+v, want staging's digest %s", res.Pins, stagingDigest)
	}
	if res.Pins[0].Tag != "" {
		t.Errorf("Tag = %q, want empty: a promotion resolves nothing from a tag", res.Pins[0].Tag)
	}
	if res.PromotedFrom != "staging" {
		t.Errorf("PromotedFrom = %q, want staging", res.PromotedFrom)
	}
	if res.Release != "1.4.2" {
		t.Errorf("Release = %q, want 1.4.2 carried over from staging", res.Release)
	}
	if res.SourceCommit != testCommit {
		t.Errorf("SourceCommit = %q, want staging's commit %q", res.SourceCommit, testCommit)
	}

	// And the overlays must now agree exactly.
	prod, _ := f.overlayOf(t, "prod").FindImage(f.image)
	staging, _ := f.overlayOf(t, "staging").FindImage(f.image)
	if prod.Digest != staging.Digest {
		t.Errorf("prod pins %q, staging pins %q; a promotion must copy verbatim", prod.Digest, staging.Digest)
	}
	if got := f.overlayOf(t, "prod").CommonAnnotations[kustomize.AnnotationRelease]; got != "1.4.2" {
		t.Errorf("prod release annotation = %q, want 1.4.2", got)
	}
}

// prod promotes from staging, so dev → prod skips the environment that was
// supposed to prove the bits. The error has to name the legal sources, because
// the whole reason to refuse is that someone is in a hurry.
func TestPromoteRefusesAnUndeclaredEdge(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "dev", stagingDigest, testCommit)

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("dev", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("dev → prod must be refused: prod promotes from staging")
	}
	for _, want := range []string{"prod does not promote from dev", "allows: staging"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	f.requireNothingWritten(t)
}

// dev promotes from nothing, and "dev does not promote from staging. allows: "
// would be a confusing way to say so.
func TestPromoteIntoAnEnvironmentThatPromotesFromNothing(t *testing.T) {
	f := newRunFixture(t)

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("staging", "dev", &stdout, &stderr))
	if err == nil {
		t.Fatal("dev declares no promotesFrom, so promoting into it must be refused")
	}
	if !strings.Contains(err.Error(), "dev promotes from nothing") {
		t.Errorf("error %q should say dev promotes from nothing", err)
	}
}

func TestPromoteRefusesTheSameEnvironment(t *testing.T) {
	f := newRunFixture(t)

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("prod", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("promoting an environment into itself must be refused")
	}
	if !strings.Contains(err.Error(), "both prod") {
		t.Errorf("error %q should name the environment", err)
	}
}

func TestPromoteRejectsAnUnknownEnvironment(t *testing.T) {
	f := newRunFixture(t)

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("nowhere", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("an environment deploy.yaml does not define must be refused")
	}
	if !strings.Contains(err.Error(), "unknown environment") {
		t.Errorf("error %q should say the environment is unknown", err)
	}
}

// Nothing has been deployed to staging yet: its overlay pins a tag, or nothing
// at all. There is no fixed set of bits to copy, and inventing one is exactly
// what promotion exists to avoid.
func TestPromoteRefusesAnUnpinnedSource(t *testing.T) {
	f := newRunFixture(t)
	// The fixture leaves staging with no images[] entry of its own.

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("an unpinned source must be refused")
	}
	if !strings.Contains(err.Error(), "staging pins nothing for") {
		t.Errorf("error %q should say staging pins nothing", err)
	}
	if !strings.Contains(err.Error(), "intropy deploy order-extractor --env staging") {
		t.Errorf("error %q should say how to fix it", err)
	}
	f.requireNothingWritten(t)
}

func TestPromoteRefusesASourcePinnedToATag(t *testing.T) {
	f := newRunFixture(t)
	f.pinTag(t, "staging", "latest")

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("a source pinned to a tag must be refused")
	}
	if !strings.Contains(err.Error(), "rather than a digest") {
		t.Errorf("error %q should explain that a tag is not a fixed set of bits", err)
	}
}

// The gate prod sets. Degraded means the bits are demonstrably not working where
// they were supposed to be proven.
func TestPromoteRefusesADegradedSource(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "staging", stagingDigest, testCommit)

	var app argocd.Application
	app.Status.Sync.Status = argocd.SyncSynced
	app.Status.Health.Status = argocd.HealthDegraded
	app.Status.Health.Message = "1/2 replicas available"
	stubArgo(t, &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-staging": &app,
	}})

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("a Degraded source must be refused")
	}
	for _, want := range []string{"Synced and Healthy", "Degraded", "1/2 replicas available", "Nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	f.requireNothingWritten(t)
}

// An unreachable ArgoCD is a warning for a deploy that has already pushed — the
// commit is the deployment. Here it is fatal: health is a precondition, and "I
// could not check" is not "it is fine".
func TestPromoteRefusesWhenArgoCDCannotBeReached(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "staging", stagingDigest, testCommit)

	stubArgo(t, &stubArgoClient{getErr: errors.New("argocd unreachable: dial tcp: connection refused")})

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("promotion must not proceed when the source's health is unknown")
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("error %q should say the application could not be read", err)
	}
	f.requireNothingWritten(t)
}

// The subtle failure the revision check exists for. staging syncs
// automatically, so it can advance between its overlay being read and its health
// being asked about; a Healthy answer then describes a *later* deployment of
// different bits. Accepting it would promote digests nothing ever ran.
func TestPromoteRefusesAHealthySourceAtAnotherRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "staging", stagingDigest, testCommit)

	// A revision that is not an ancestor of, or equal to, the overlay's.
	unrelated := gittest.Run(t, f.cloneOrigin(t), "rev-list", "--max-parents=0", "HEAD")
	stubArgo(t, &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-staging": healthyApp(unrelated),
	}})

	var stdout, stderr bytes.Buffer
	err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("a Healthy source at an earlier revision must be refused")
	}
	for _, want := range []string{"is healthy, but at revision", "does not show that these bits ran"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	f.requireNothingWritten(t)
}

// staging has been deployed to since, so ArgoCD reports a descendant of the
// revision that pinned the digests. That still proves these bits ran there.
func TestPromoteAcceptsADescendantRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "staging", stagingDigest, testCommit)
	pinnedAt := gittest.Run(t, f.cloneOrigin(t), "log", "-1", "--format=%H", "--",
		"domains/orders/order-flow/order-extractor/overlays/staging")

	// An unrelated later commit on the branch.
	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	gittest.Commit(t, clone, "notes.txt", "later\n", "unrelated change")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
	descendant := gittest.Run(t, clone, "rev-parse", "HEAD")

	if descendant == pinnedAt {
		t.Fatal("fixture did not advance the branch")
	}
	stubArgo(t, &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-staging": healthyApp(descendant),
	}})

	var stdout, stderr bytes.Buffer
	if err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr)); err != nil {
		t.Fatalf("a descendant revision should be accepted: %v\nstderr: %s", err, stderr.String())
	}
}

// prod syncs manually, so the promotion records intent and stops. The follow-up
// it prints has to be a command that exists.
func TestPromoteToManualSyncStopsAtTheCommit(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "staging", stagingDigest, testCommit, "1.4.2")
	f.healthySource(t, "staging")

	var stdout, stderr bytes.Buffer
	if err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr)); err != nil {
		t.Fatalf("promote: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{"prod syncs manually", "intropy deploy sync order-extractor --env prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout should contain %q:\n%s", want, out)
		}
	}
}

// The subject the release story asks for, and the trailer that tells a promotion
// apart from a deployment in the log.
func TestPromoteCommitNamesBothVersions(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "prod", testDigest, "0123456789abcdef0123456789abcdef01234567", "1.4.1")
	f.pinOverlayRelease(t, "staging", stagingDigest, testCommit, "1.4.2")
	f.healthySource(t, "staging")

	var stdout, stderr bytes.Buffer
	if err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr)); err != nil {
		t.Fatalf("promote: %v\nstderr: %s", err, stderr.String())
	}

	subject := gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%s", "main")
	if want := "deploy(order-extractor): prod 1.4.1 → 1.4.2"; subject != want {
		t.Errorf("subject = %q, want %q", subject, want)
	}

	trailers := gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%(trailers:only=true)", "main")
	for _, want := range []string{
		TrailerPromotedFrom + ": staging",
		TrailerRelease + ": 1.4.2",
		TrailerDigest + ": " + stagingDigest,
		TrailerEnvironment + ": prod",
	} {
		if !strings.Contains(trailers, want) {
			t.Errorf("trailers should contain %q:\n%s", want, trailers)
		}
	}
}

func TestPromotePlanOnlyWritesNothing(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "staging", stagingDigest, testCommit)
	f.healthySource(t, "staging")

	var stdout, stderr bytes.Buffer
	opts := f.promoteOptions("staging", "prod", &stdout, &stderr)
	opts.PlanOnly = true

	if err := Promote(context.Background(), opts); err != nil {
		t.Fatalf("promote --plan: %v\nstderr: %s", err, stderr.String())
	}

	if !strings.Contains(stdout.String(), stagingDigest) {
		t.Errorf("the plan should show the digest being promoted:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "nothing was committed") {
		t.Errorf("stderr should say nothing was committed:\n%s", stderr.String())
	}
	f.requireNothingWritten(t)
}

// The plan says where the digests came from, in place of the promotesFrom
// comparison a deploy prints — a promotion knows its source, so restating it as
// a comparison would be noise.
func TestPromotePlanSaysWhereTheDigestsCameFrom(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "staging", stagingDigest, testCommit, "1.4.2")
	f.healthySource(t, "staging")

	var stdout, stderr bytes.Buffer
	opts := f.promoteOptions("staging", "prod", &stdout, &stderr)
	opts.PlanOnly = true
	if err := Promote(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	if !strings.Contains(out, "copied from staging, which runs release 1.4.2") {
		t.Errorf("the plan should name the source and its version:\n%s", out)
	}
	if !strings.Contains(out, "orders-order-flow-order-extractor-staging is synced and healthy") {
		t.Errorf("the plan should report the health check that ran:\n%s", out)
	}
}

// Promoting again once prod already runs those digests. The no-op must not
// produce an empty commit.
func TestPromoteAlreadyThereIsANoOp(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "prod", stagingDigest, testCommit, "1.4.2")
	f.pinOverlayRelease(t, "staging", stagingDigest, testCommit, "1.4.2")
	f.healthySource(t, "staging")
	before := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")

	var stdout, stderr bytes.Buffer
	if err := Promote(context.Background(), f.promoteOptions("staging", "prod", &stdout, &stderr)); err != nil {
		t.Fatalf("promote: %v\nstderr: %s", err, stderr.String())
	}

	if !strings.Contains(stdout.String(), "nothing to do") {
		t.Errorf("stdout should report a no-op:\n%s", stdout.String())
	}
	if got := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main"); got != before {
		t.Errorf("main moved to %q; a no-op must not commit", got)
	}
}

// An environment with no requireSourceHealthy must not need ArgoCD at all.
func TestPromoteWithoutTheHealthGateNeedsNoArgoCD(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "dev", stagingDigest, testCommit)
	stubArgo(t, &stubArgoClient{getErr: errors.New("Get must not be called for staging")})

	var stdout, stderr bytes.Buffer
	opts := f.promoteOptions("dev", "staging", &stdout, &stderr)
	opts.NoWait = true

	if err := Promote(context.Background(), opts); err != nil {
		t.Fatalf("staging does not set requireSourceHealthy: %v\nstderr: %s", err, stderr.String())
	}
	staging, _ := f.overlayOf(t, "staging").FindImage(f.image)
	if staging.Digest != stagingDigest {
		t.Errorf("staging pins %q, want dev's digest %q", staging.Digest, stagingDigest)
	}
}
