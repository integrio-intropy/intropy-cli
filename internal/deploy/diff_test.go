package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func (f runFixture) diffOptions(env string, stdout, stderr *bytes.Buffer) DiffOptions {
	return DiffOptions{
		Component:   "order-extractor",
		Environment: env,
		CacheRoot:   f.cacheRoot,
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

// renderedDeployment is one resource as ArgoCD returns it: a JSON document, on
// one line, with the digest as the only interesting part.
func renderedDeployment(digest string) string {
	return `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"order-extractor","namespace":"integrations"},` +
		`"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"app","image":"harbor.intropy.io/integrations/order-extractor@` + digest + `"}]}}}}`
}

func renderedCronJob() string {
	return `{"apiVersion":"batch/v1","kind":"CronJob","metadata":{"name":"order-reaper","namespace":"integrations"},` +
		`"spec":{"schedule":"0 3 * * *"}}`
}

func decodeDiffResult(t *testing.T, b []byte) DiffResult {
	t.Helper()
	var res DiffResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, b)
	}
	return res
}

// pinOverlayTrailers pins an overlay with a commit carrying the deployment
// trailer block, which is what a real deploy or promotion leaves behind.
func (f runFixture) pinOverlayTrailers(t *testing.T, env, digest, commit, message string) {
	t.Helper()
	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")

	rel := filepath.Join("domains", "orders", "order-flow", "order-extractor", "overlays", env, "kustomization.yaml")
	gittest.WriteFile(t, filepath.Join(clone, rel),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\n"+
			"images:\n  - name: "+f.image+"\n    digest: "+digest+"\n")

	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "--quiet", "-m", message)
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
}

// commitUnrelated pushes a commit that does not touch the overlay, so the branch
// head stops being the pending revision.
func (f runFixture) commitUnrelated(t *testing.T, note string) string {
	t.Helper()
	clone := f.cloneOrigin(t)
	gittest.Run(t, clone, "config", "user.email", "test@example.com")
	gittest.Run(t, clone, "config", "user.name", "Test")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	gittest.Commit(t, clone, "notes.txt", note+"\n", "unrelated change")
	gittest.Run(t, clone, "push", "--quiet", "origin", "main")
	return gittest.Run(t, clone, "rev-parse", "HEAD")
}

// The command: the approver sees the resource change between what prod runs and
// what a sync would apply, and is told the revision to hand to sync.
func TestDiffShowsWhatASyncWouldApply(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", testDigest, testCommit)
	oldRevision := f.overlayRevision(t, "prod")
	f.pinOverlay(t, "prod", stagingDigest, testReleaseCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp(oldRevision),
		},
		manifests: map[string][]string{
			oldRevision: {renderedDeployment(testDigest)},
			pending:     {renderedDeployment(stagingDigest)},
		},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	// The rendered difference, not a one-line image pin.
	if !strings.Contains(out, "@@") {
		t.Errorf("stdout should carry a unified diff:\n%s", out)
	}
	if !strings.Contains(out, "-          image: "+f.image+"@"+testDigest) {
		t.Errorf("stdout should show the digest being replaced:\n%s", out)
	}
	if !strings.Contains(out, "+          image: "+f.image+"@"+stagingDigest) {
		t.Errorf("stdout should show the digest being applied:\n%s", out)
	}
	// Both revisions, so the approver knows what is being compared.
	if !strings.Contains(out, git.ShortSHA(pending)) || !strings.Contains(out, git.ShortSHA(oldRevision)) {
		t.Errorf("stdout should name both revisions:\n%s", out)
	}
	// The handoff to sync, with the full sha: sameRevision is a prefix match.
	if !strings.Contains(out, "--revision "+pending) {
		t.Errorf("stdout should offer the sync command with the full pending sha:\n%s", out)
	}
	// Block YAML, not the single-line JSON ArgoCD returned.
	if strings.Contains(out, `{"apiVersion"`) {
		t.Errorf("the render should be normalised to block YAML:\n%s", out)
	}
}

// Load-bearing: the diff and the sync must be about the same commit. A diff of a
// revision other than the one that will be applied is worse than no diff.
func TestDiffRendersThePendingRevisionNotTheBranchHead(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")
	head := f.commitUnrelated(t, "later")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
		},
		manifests: map[string][]string{pending: {renderedDeployment(stagingDigest)}},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	for _, call := range stub.rendered {
		if call.revision == head {
			t.Errorf("the branch head %s must never be rendered; ArgoCD was asked for %+v", head, stub.rendered)
		}
	}
	if !containsRevision(stub.rendered, pending) {
		t.Errorf("the pending revision %s should have been rendered, got %+v", pending, stub.rendered)
	}
}

// An application that has never synced has no baseline revision. Passing the
// empty string to ArgoCD would render the pending tree as its own baseline and
// report that nothing changes — immediately before the first apply of everything.
func TestDiffNeverIssuesAnEmptyRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp(""),
		},
		manifests: map[string][]string{pending: {renderedDeployment(stagingDigest)}},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	if len(stub.rendered) != 1 {
		t.Fatalf("only the pending revision should be rendered, got %+v", stub.rendered)
	}
	if stub.rendered[0].revision != pending {
		t.Errorf("rendered %q, want the pending revision %q", stub.rendered[0].revision, pending)
	}

	out := stdout.String()
	if !strings.Contains(out, "never been synced") {
		t.Errorf("stdout should say the application has never been synced:\n%s", out)
	}
	// Everything is new, so the whole render is an addition.
	if !strings.Contains(out, "+apiVersion: apps/v1") {
		t.Errorf("the diff should add every resource:\n%s", out)
	}
	// The baseline side of the diff carries no revision, rather than an empty one.
	overlay := "domains/orders/order-flow/order-extractor/overlays/prod"
	if !strings.Contains(out, "--- "+overlay+" (never synced)") {
		t.Errorf("the baseline label should name no revision:\n%s", out)
	}
	if !strings.Contains(out, "+++ "+overlay+" @ "+git.ShortSHA(pending)+" (will be applied)") {
		t.Errorf("the pending label should name the pending revision:\n%s", out)
	}
}

// ArgoCD already holds the pending revision and is healthy: there is nothing to
// review, and two renders would be spent proving it.
func TestDiffAlreadyAppliedIsANoOp(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-prod": healthyApp(pending),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	if len(stub.rendered) != 0 {
		t.Errorf("nothing should have been rendered, got %+v", stub.rendered)
	}
	out := stdout.String()
	if !strings.Contains(out, "nothing to review") {
		t.Errorf("stdout should report a no-op:\n%s", out)
	}
	if strings.Contains(out, "@@") {
		t.Errorf("there is no diff to show:\n%s", out)
	}
}

// A deletion in the diff is not a deletion in the cluster: Sync does not prune.
// Approving "we are retiring the old job" on this diff would be wrong.
func TestDiffWarnsThatSyncDoesNotPrune(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", testDigest, testCommit)
	oldRevision := f.overlayRevision(t, "prod")
	f.pinOverlay(t, "prod", stagingDigest, testReleaseCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp(oldRevision),
		},
		manifests: map[string][]string{
			oldRevision: {renderedDeployment(testDigest), renderedCronJob()},
			pending:     {renderedDeployment(stagingDigest)},
		},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "does not prune") {
		t.Errorf("stdout should warn that the removed resource stays:\n%s", out)
	}
	// Named, so the approver knows which resource the caveat is about.
	if !strings.Contains(out, "CronJob integrations/order-reaper") {
		t.Errorf("stdout should name the resource that will stay:\n%s", out)
	}
}

// Both sides come from git, so drift is outside what was compared. Worth saying
// even when the two renders are identical, which otherwise reads as "safe".
func TestDiffWarnsAboutDriftEvenWithAnEmptyDiff(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", testDigest, testCommit)
	oldRevision := f.overlayRevision(t, "prod")
	f.pinOverlay(t, "prod", stagingDigest, testReleaseCommit)
	pending := f.overlayRevision(t, "prod")

	// Identical renders at both revisions.
	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp(oldRevision),
		},
		manifests: map[string][]string{
			oldRevision: {renderedDeployment(testDigest)},
			pending:     {renderedDeployment(testDigest)},
		},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "OutOfSync") || !strings.Contains(out, "outside git") {
		t.Errorf("stdout should warn that applying also reverts drift:\n%s", out)
	}
	if !strings.Contains(out, "renders identically") {
		t.Errorf("stdout should say why the diff is empty:\n%s", out)
	}
}

// ArgoCD has applied something newer than the pending overlay commit, so syncing
// it would render the tree as it stood then — a rollback of whatever came after.
func TestDiffWarnsWhenSyncingWouldGoBackwards(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")
	descendant := f.commitUnrelated(t, "a later change to something the overlay includes")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			// Holds the descendant, but is not settled, so the no-op path is not taken.
			"orders-order-flow-order-extractor-prod": outOfSyncApp(descendant),
		},
		manifests: map[string][]string{
			descendant: {renderedDeployment(stagingDigest), renderedCronJob()},
			pending:    {renderedDeployment(stagingDigest)},
		},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "at or beyond the pending commit") {
		t.Errorf("stdout should warn that syncing would go backwards:\n%s", out)
	}
}

// ArgoCD's applied revision is an input here, not an observation. Unlike a deploy,
// which has already pushed, there is no useful degraded answer.
func TestDiffFailsWhenArgoCDCannotBeReached(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)

	stubArgo(t, &stubArgoClient{getErr: errors.New("argocd unreachable: dial tcp: connection refused")})

	var stdout, stderr bytes.Buffer
	err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("an unreachable ArgoCD must fail a diff")
	}
	if !strings.Contains(err.Error(), "orders-order-flow-order-extractor-prod") {
		t.Errorf("the error should name the application: %v", err)
	}
}

// A render that fails at the pending revision means a sync of it would fail too,
// which is the most useful thing this command can report.
func TestDiffFailsWhenThePendingRevisionCannotBeRendered(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
		},
		manifestsErr: errors.New("rpc error: code = Unknown desc = accumulating resources"),
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr))
	if err == nil {
		t.Fatal("a failed render must fail the diff")
	}
	// Which side broke, and at which revision.
	if !strings.Contains(err.Error(), git.ShortSHA(pending)) || !strings.Contains(err.Error(), "a sync would apply") {
		t.Errorf("the error should name the failing side: %v", err)
	}
}

// The commit's own account of itself: an approver's second question is who asked
// for this, and where the bits came from.
func TestDiffReportsTheCommitProvenance(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayTrailers(t, "prod", stagingDigest, testCommit,
		"deploy(order-extractor): prod → 1.4.2\n\n"+
			TrailerRelease+": 1.4.2\n"+
			TrailerPromotedFrom+": staging\n"+
			TrailerSourceCommit+": "+testReleaseCommit+"\n"+
			TrailerBy+": approver@example.com\n")
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
		},
		manifests: map[string][]string{pending: {renderedDeployment(stagingDigest)}},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"deploy(order-extractor): prod → 1.4.2",
		"release 1.4.2",
		"promoted from staging",
		"by approver@example.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout should report %q:\n%s", want, out)
		}
	}
}

// This command reads and prints. It must leave the shared checkout as it found it.
func TestDiffLeavesTheCheckoutClean(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
		},
		manifests: map[string][]string{pending: {renderedDeployment(stagingDigest)}},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	if err := Diff(context.Background(), f.diffOptions("prod", &stdout, &stderr)); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	root := gitops.CheckoutDir(f.cacheRoot, f.gitopsOrigin)
	g := git.Client{Runner: command.ExecRunner{}, Dir: root}
	dirty, err := g.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Errorf("Diff must not leave the cached checkout dirty, got %v", dirty)
	}
	f.requireNothingWritten(t)
}

// The diff travels inside the JSON here rather than beside it, so colour must be
// off however the caller asked for it.
func TestDiffJSONIsParseableAndUncoloured(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", testDigest, testCommit)
	oldRevision := f.overlayRevision(t, "prod")
	f.pinOverlay(t, "prod", stagingDigest, testReleaseCommit)
	pending := f.overlayRevision(t, "prod")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp(oldRevision),
		},
		manifests: map[string][]string{
			oldRevision: {renderedDeployment(testDigest), renderedCronJob()},
			pending:     {renderedDeployment(stagingDigest)},
		},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	opts := f.diffOptions("prod", &stdout, &stderr)
	opts.OutputFormat = OutputJSON
	opts.Color = true
	if err := Diff(context.Background(), opts); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}

	if strings.Contains(stdout.String(), "\x1b") {
		t.Errorf("JSON output must never be coloured:\n%s", stdout.String())
	}
	res := decodeDiffResult(t, stdout.Bytes())
	if res.Pending != pending {
		t.Errorf("pending = %q, want the full sha %q", res.Pending, pending)
	}
	if res.Synced != oldRevision {
		t.Errorf("synced = %q, want %q", res.Synced, oldRevision)
	}
	if res.Applied {
		t.Error("applied should be false: ArgoCD holds an older revision")
	}
	if !res.Changed || res.Diff == "" {
		t.Errorf("changed/diff should report the difference: %+v", res)
	}
	if res.SyncPolicy != gitops.SyncManual {
		t.Errorf("syncPolicy = %q, want %q", res.SyncPolicy, gitops.SyncManual)
	}
	if len(res.RemovedResources) != 1 {
		t.Errorf("removedResources = %v, want the CronJob", res.RemovedResources)
	}
}

func TestDiffRejectsAnUnknownEnvironment(t *testing.T) {
	f := newRunFixture(t)

	var stdout, stderr bytes.Buffer
	err := Diff(context.Background(), f.diffOptions("qa", &stdout, &stderr))
	if err == nil {
		t.Fatal("an environment deploy.yaml does not define must be refused")
	}
	if !strings.Contains(err.Error(), "qa") {
		t.Errorf("the error should name the environment: %v", err)
	}
}

// The property the whole two-command flow rests on: what the diff reports as
// pending is exactly what the sync applies.
func TestDiffAndSyncAgreeOnThePendingRevision(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "prod", stagingDigest, testCommit)
	pending := f.overlayRevision(t, "prod")
	f.commitUnrelated(t, "the branch moves on")

	stub := &stubArgoClient{
		get: map[string]*argocd.Application{
			"orders-order-flow-order-extractor-prod": outOfSyncApp("0000000000000000000000000000000000000000"),
		},
		manifests: map[string][]string{pending: {renderedDeployment(stagingDigest)}},
	}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	opts := f.diffOptions("prod", &stdout, &stderr)
	opts.OutputFormat = OutputJSON
	if err := Diff(context.Background(), opts); err != nil {
		t.Fatalf("diff: %v\nstderr: %s", err, stderr.String())
	}
	reviewed := decodeDiffResult(t, stdout.Bytes()).Pending

	// Feed the reviewed sha straight back, as the printed follow-up tells you to.
	stdout.Reset()
	stderr.Reset()
	syncOpts := f.syncOptions("prod", &stdout, &stderr)
	syncOpts.Revision = reviewed
	if err := Sync(context.Background(), syncOpts); err != nil {
		t.Fatalf("sync: %v\nstderr: %s", err, stderr.String())
	}

	if len(stub.synced) != 1 {
		t.Fatalf("sync calls = %+v, want exactly one", stub.synced)
	}
	if stub.synced[0].revision != reviewed {
		t.Errorf("synced %q but the diff reviewed %q", stub.synced[0].revision, reviewed)
	}
}

func containsRevision(calls []syncCall, revision string) bool {
	for _, call := range calls {
		if call.revision == revision {
			return true
		}
	}
	return false
}
