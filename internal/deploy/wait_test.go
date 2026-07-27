package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// stubArgoClient stands in for the ArgoCD client.
type stubArgoClient struct {
	// app and err are what Wait returns; seen records what it was asked for.
	app  *argocd.Application
	err  error
	seen argocd.WaitOptions

	// get answers Get per application name, and getErr overrides it. An
	// application with no entry is reported missing, as ArgoCD would.
	get    map[string]*argocd.Application
	getErr error

	// syncErr fails Sync; synced records every revision it was asked to apply,
	// which is what proves a sync targeted the reviewed commit.
	syncErr error
	synced  []syncCall

	// manifests answers Manifests per revision, and manifestsErr overrides it.
	// rendered records every revision asked for, which is what proves a diff
	// rendered the pending commit rather than the branch head.
	manifests    map[string][]string
	manifestsErr error
	rendered     []syncCall
}

type syncCall struct{ app, revision string }

func (s *stubArgoClient) Wait(_ context.Context, opts argocd.WaitOptions) (*argocd.Application, error) {
	s.seen = opts
	return s.app, s.err
}

func (s *stubArgoClient) Get(_ context.Context, app string) (*argocd.Application, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if got, found := s.get[app]; found {
		return got, nil
	}
	return nil, fmt.Errorf("%w: %s", argocd.ErrAppNotFound, app)
}

func (s *stubArgoClient) Sync(_ context.Context, app, revision string) error {
	s.synced = append(s.synced, syncCall{app: app, revision: revision})
	return s.syncErr
}

func (s *stubArgoClient) Manifests(_ context.Context, app, revision string) (*argocd.ManifestResponse, error) {
	s.rendered = append(s.rendered, syncCall{app: app, revision: revision})
	if s.manifestsErr != nil {
		return nil, s.manifestsErr
	}
	// A revision with no entry renders nothing, which is what an environment that
	// did not exist then looks like.
	return &argocd.ManifestResponse{Manifests: s.manifests[revision], Revision: revision}, nil
}

// stubArgo replaces the client factory and supplies credentials, so these tests
// exercise the wiring rather than the HTTP layer.
func stubArgo(t *testing.T, w *stubArgoClient) {
	t.Helper()
	t.Setenv("ARGOCD_SERVER", "argocd.test.example.com")
	t.Setenv("ARGOCD_AUTH_TOKEN", "test-token")

	original := NewArgoClient
	NewArgoClient = func(argocd.Options) (ArgoClient, error) { return w, nil }
	t.Cleanup(func() { NewArgoClient = original })
}

func healthyApp(revision string) *argocd.Application {
	var app argocd.Application
	app.Status.Sync.Status = argocd.SyncSynced
	app.Status.Sync.Revision = revision
	app.Status.Health.Status = argocd.HealthHealthy
	return &app
}

func TestRunWaitsAndReportsSynced(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	waiter := &stubArgoClient{}
	stubArgo(t, waiter)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.OutputFormat = OutputJSON

	// The revision is only known after the push, so the app is produced lazily.
	waiter.app = nil
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	revision := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")
	if waiter.seen.Revision != revision {
		t.Errorf("waited on %q, want the pushed revision %q", waiter.seen.Revision, revision)
	}
	if waiter.seen.App != "orders-order-flow-order-extractor-dev" {
		t.Errorf("waited on app %q", waiter.seen.App)
	}
	// The ancestor seam must be supplied, or a descendant revision would hang.
	if waiter.seen.Contains == nil {
		t.Error("Contains should be supplied so a descendant revision satisfies the wait")
	}
}

func TestRunReportsArgoStateInJSON(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	waiter := &stubArgoClient{}
	stubArgo(t, waiter)
	// Answer with whatever revision is asked for.
	waiter.app = healthyApp("")

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.OutputFormat = OutputJSON
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	res := decodeResult(t, stdout.Bytes())
	if res.SyncStatus != argocd.SyncSynced || res.HealthStatus != argocd.HealthHealthy {
		t.Errorf("sync/health = %q/%q, want ArgoCD's reported state", res.SyncStatus, res.HealthStatus)
	}
	if !res.Applied {
		t.Error("Applied should be true")
	}
}

// The commit is the deployment. Being unable to reach ArgoCD after a successful
// push does not undo it, so this warns and succeeds rather than reporting a
// failure that did not happen.
func TestRunUnreachableArgoIsAWarningNotAFailure(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	stubArgo(t, &stubArgoClient{err: fmt.Errorf("%w: dial tcp: connection refused", argocd.ErrUnreachable)})

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("an unreachable ArgoCD must not fail the deploy: %v", err)
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr should carry a warning:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "deployment stands") {
		t.Errorf("stderr should say the deployment still stands:\n%s", stderr.String())
	}
	// And the push really happened.
	if !strings.Contains(gittest.Run(t, f.gitopsOrigin, "log", "-1", "--format=%s", "main"), "deploy(order-extractor)") {
		t.Error("the commit should be on the origin regardless")
	}
}

// A timeout is a real failure: the change was pushed but never converged, and a
// script must be able to tell that from success.
func TestRunTimeoutIsAFailure(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	stubArgo(t, &stubArgoClient{err: &argocd.TimeoutError{App: "orders-order-flow-order-extractor-dev", Revision: testCommit}})

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), f.options(&stdout, &stderr))
	if err == nil {
		t.Fatal("a timeout should fail the command")
	}
	if _, ok := errors.AsType[*argocd.TimeoutError](err); !ok {
		t.Errorf("error %v should be *argocd.TimeoutError", err)
	}
}

func TestRunNoWaitSkipsArgo(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	waiter := &stubArgoClient{err: errors.New("should not be called")}
	stubArgo(t, waiter)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.NoWait = true

	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if waiter.seen.App != "" {
		t.Error("--no-wait should not consult ArgoCD")
	}
}

// A manual-sync environment is gated in ArgoCD; there is no sync to wait for, so
// waiting would hang until the timeout.
func TestRunManualSyncNeverWaits(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	waiter := &stubArgoClient{err: errors.New("should not be called")}
	stubArgo(t, waiter)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "prod"

	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if waiter.seen.App != "" {
		t.Error("a manual-sync environment should not wait for ArgoCD")
	}
	if !strings.Contains(stdout.String(), "syncs manually") {
		t.Errorf("stdout should explain why:\n%s", stdout.String())
	}
}

// Missing credentials are a setup problem, not a failed deployment: the push
// already succeeded.
func TestRunWithoutArgoCredentialsWarns(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	t.Setenv("ARGOCD_CONFIG", t.TempDir()+"/absent")
	t.Setenv("ARGOCD_SERVER", "")
	t.Setenv("ARGOCD_AUTH_TOKEN", "")

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), f.options(&stdout, &stderr)); err != nil {
		t.Fatalf("missing ArgoCD credentials must not fail the deploy: %v", err)
	}
	if !strings.Contains(stderr.String(), "not waiting for ArgoCD") {
		t.Errorf("stderr should explain that the wait was skipped:\n%s", stderr.String())
	}
}

// revisionContains is what stops a descendant revision hanging the wait.
func TestRevisionContains(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	contains := revisionContains(f.repo)

	head := gittest.HEAD(t, f.repo.Root)

	if ok, err := contains(ctx, head, head); err != nil || !ok {
		t.Errorf("contains(head, head) = %v, %v; want true", ok, err)
	}
	if ok, err := contains(ctx, head, ""); err != nil || ok {
		t.Errorf("an empty reported revision means not yet, got %v, %v", ok, err)
	}

	// A descendant on the origin: ours is an ancestor of it, so it counts — and
	// finding that out requires a fetch, since the commit is not local yet.
	gittest.Commit(t, f.origin, "later.txt", "later\n", "a later commit")
	later := gittest.HEAD(t, f.origin)
	if ok, err := contains(ctx, head, later); err != nil || !ok {
		t.Errorf("contains(head, descendant) = %v, %v; want true after a fetch", ok, err)
	}

	// An unknown revision is "not yet" rather than an error: failing the wait on
	// a revision we cannot inspect would be worse than waiting.
	if ok, err := contains(ctx, head, "0000000000000000000000000000000000000000"); err != nil || ok {
		t.Errorf("contains(head, unknown) = %v, %v; want false, nil", ok, err)
	}
}

// Staging syncs automatically, so the command waits and reports health — the
// back half of the staging story.
func TestRunStagingWaitsAndReportsHealth(t *testing.T) {
	f := newRunFixture(t)
	stubDigest(t, testDigest)

	w := &stubArgoClient{app: healthyApp("")}
	stubArgo(t, w)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Environment = "staging"
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("staging syncs automatically, so this should converge: %v\nstderr: %s", err, stderr.String())
	}

	if w.seen.App != "orders-order-flow-order-extractor-staging" {
		t.Errorf("waited on %q, want the staging application", w.seen.App)
	}
	if !strings.Contains(stdout.String(), "synced and healthy") {
		t.Errorf("stdout should report health:\n%s", stdout.String())
	}
}
