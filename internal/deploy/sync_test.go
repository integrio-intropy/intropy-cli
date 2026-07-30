//go:build integration

package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func (f runFixture) syncOptions(env string, stdout, stderr *bytes.Buffer) SyncOptions {
	return SyncOptions{
		Component:   "order-extractor",
		Environment: env,
		CacheRoot:   f.cacheRoot,
		NoWait:      true,
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

// overlayRevision is the commit that last changed an environment's overlay — the
// pending intent, and the revision a sync must target.
func (f runFixture) overlayRevision(t *testing.T, env string) string {
	t.Helper()
	return gittest.Run(t, f.cloneOrigin(t), "log", "-1", "--format=%H", "--",
		"domains/orders/order-flow/order-extractor/overlays/"+env)
}

func decodeSyncResult(t *testing.T, b []byte) SyncResult {
	t.Helper()
	var res SyncResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, b)
	}
	return res
}

// outOfSyncApp is a manual-sync application holding an older revision: the state
// prod is in after intent has been pushed but nobody has applied it.
func outOfSyncApp(revision string) *argocd.Application {
	var app argocd.Application
	app.Status.Sync.Status = "OutOfSync"
	app.Status.Sync.Revision = revision
	app.Status.Health.Status = argocd.HealthHealthy
	return &app
}

// The command: ArgoCD is asked to apply the exact commit that carries the
// pending change.
func TestSyncTargetsTheOverlayRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	// A later unrelated commit, so the branch head is not the pending revision.
	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	gittest.Commit(t, clone, "notes.txt", "later\n", "unrelated change")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
	head := gittest.Run(t, clone, "rev-parse", "HEAD")

	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Sync(context.Background(), f.syncOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, stderr.String())
	}

	if len(stub.synced) != 1 {
		t.Fatalf("sync calls = %+v, want exactly one", stub.synced)
	}
	got := stub.synced[0]
	if got.app != "orders-order-flow-order-extractor-prod" {
		t.Errorf("synced app = %q", got.app)
	}
	// The reviewed commit, not the branch head: syncing the head would apply the
	// unrelated commit nobody looked at.
	if got.revision != pending {
		t.Errorf("synced revision = %q, want the overlay's commit %q (branch head is %q)", got.revision, pending, head)
	}
}

// ArgoCD already holds the pending revision and is healthy. Syncing again would
// be a no-op that reads as an action.
func TestSyncAlreadyAppliedIsANoOp(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": healthyApp(pending),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Sync(context.Background(), f.syncOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, stderr.String())
	}

	if len(stub.synced) != 0 {
		t.Errorf("nothing should have been synced, got %+v", stub.synced)
	}
	if !strings.Contains(stdout.String(), "nothing to do") {
		t.Errorf("stdout should report a no-op:\n%s", stdout.String())
	}
}

// ArgoCD has synced a descendant of the pending revision, so the change is
// already in. Asking again would still be a no-op.
func TestSyncADescendantCountsAsApplied(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)

	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	gittest.Commit(t, clone, "notes.txt", "later\n", "unrelated change")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
	descendant := gittest.Run(t, clone, "rev-parse", "HEAD")

	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": healthyApp(descendant),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Sync(context.Background(), f.syncOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatal(err)
	}
	if len(stub.synced) != 0 {
		t.Errorf("a descendant revision means it is already applied, got %+v", stub.synced)
	}
}

// The reviewed-revision guard. Somebody deployed again between the diff being
// read and the sync being run, so the approval was given for a different change.
func TestSyncRefusesAStaleReviewedRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)

	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	opts := f.syncOptions("prod", &stdout, &stderr)
	opts.Revision = "1234567890abcdef1234567890abcdef12345678"

	err := Sync(context.Background(), opts)
	if err == nil {
		t.Fatal("a sync of a revision other than the pending one must be refused")
	}
	for _, want := range []string{"pending change is", "has advanced since you reviewed it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	if len(stub.synced) != 0 {
		t.Errorf("nothing should have been synced, got %+v", stub.synced)
	}
}

// The same revision, abbreviated the way a log prints it, must be accepted:
// refusing what someone copied out of `git log --oneline` would make the guard
// unusable.
func TestSyncAcceptsAnAbbreviatedReviewedRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	opts := f.syncOptions("prod", &stdout, &stderr)
	opts.Revision = pending[:8]

	if err := Sync(context.Background(), opts); err != nil {
		t.Fatalf("an abbreviated sha of the pending revision should be accepted: %v", err)
	}
	if len(stub.synced) != 1 || stub.synced[0].revision != pending {
		t.Errorf("synced = %+v, want the full pending revision", stub.synced)
	}
}

// An unreachable ArgoCD is a warning for a deploy, whose commit is the
// deployment. For a sync it is fatal: the API call *is* the action, so there is
// nothing to fall back on.
func TestSyncFailsWhenArgoCDCannotBeReached(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	stubArgo(t, &stubArgoClient{getErr: errors.New("argocd unreachable: dial tcp: connection refused")})

	var stdout, stderr bytes.Buffer
	err := Sync(context.Background(), f.syncOptions("prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("a sync must fail when ArgoCD cannot be reached")
	}
	if !strings.Contains(err.Error(), "cannot read orders-order-flow-order-extractor-prod") {
		t.Errorf("error %q should name the application", err)
	}
}

// RBAC denied the sync: the caller does not have prod rights. The gate working
// as designed, and a failure rather than a warning.
func TestSyncSurfacesARejectedSync(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)

	stubArgo(t, &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
		},
		syncErr: errors.New("permission denied: applications, sync, prod/order-extractor"),
	})

	var stdout, stderr bytes.Buffer
	err := Sync(context.Background(), f.syncOptions("prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("a denied sync must fail")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error %q should carry ArgoCD's reason", err)
	}
}

// An environment that was onboarded but never deployed to still has the
// onboarding commit as its overlay revision, so it syncs that. The point is that
// it syncs *something specific* — a sync must never be issued with an empty
// revision, which ArgoCD would read as "whatever the branch holds".
func TestSyncNeverIssuesAnEmptyRevision(t *testing.T) {
	f := newRunFixture(t)
	// No deploy to prod at all: only the onboarding commit touched its overlay.
	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Sync(context.Background(), f.syncOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, stderr.String())
	}

	if len(stub.synced) != 1 {
		t.Fatalf("sync calls = %+v, want exactly one", stub.synced)
	}
	if got := stub.synced[0].revision; got != f.overlayRevision(t, "prod") {
		t.Errorf("synced revision = %q, want the onboarding commit", got)
	}
	if stub.synced[0].revision == "" {
		t.Error("an empty revision lets ArgoCD choose, which defeats the whole command")
	}
}

func TestSyncRejectsAnUnknownEnvironment(t *testing.T) {
	f := newRunFixture(t)

	var stdout, stderr bytes.Buffer
	err := Sync(context.Background(), f.syncOptions("nowhere", &stdout, &stderr))
	if err == nil {
		t.Fatal("an environment deploy.yaml does not define must be refused")
	}
	if !strings.Contains(err.Error(), "unknown environment") {
		t.Errorf("error %q should say the environment is unknown", err)
	}
}

func TestSyncJSONReportsWhatWasSynced(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stubArgo(t, &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
	}})

	var stdout, stderr bytes.Buffer
	opts := f.syncOptions("prod", &stdout, &stderr)
	opts.OutputFormat = OutputJSON

	if err := Sync(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	res := decodeSyncResult(t, stdout.Bytes())
	if res.Revision != pending {
		t.Errorf("Revision = %q, want %q", res.Revision, pending)
	}
	if !res.Requested {
		t.Error("Requested should be true when a sync was asked for")
	}
	if res.Environment != "prod" || res.AppName != "orders-order-flow-order-extractor-prod" {
		t.Errorf("result = %+v", res)
	}
	if res.SyncPolicy != "manual" {
		t.Errorf("SyncPolicy = %q, want manual", res.SyncPolicy)
	}
}

func TestSameRevision(t *testing.T) {
	full := "1234567890abcdef1234567890abcdef12345678"
	cases := []struct {
		a, b string
		want bool
	}{
		{full, full, true},
		{full[:7], full, true},
		{full[:12], full, true},
		{full, full[:8], true},
		{"1234567", "9999999999", false},
		// Too short to be unambiguous: git itself will not resolve fewer than
		// four, and accepting a prefix this loose would match half the repository.
		{"123", full, false},
	}
	for _, tc := range cases {
		if got := sameRevision(tc.a, tc.b); got != tc.want {
			t.Errorf("sameRevision(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
