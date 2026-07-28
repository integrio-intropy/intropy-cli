package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func (f runFixture) statusOptions(stdout, stderr *bytes.Buffer) StatusOptions {
	return StatusOptions{
		Component: "order-extractor",
		CacheRoot: f.cacheRoot,
		Stdout:    stdout,
		Stderr:    stderr,
	}
}

// pinAll puts the same digest in all three environments — the state the release
// process is supposed to end in.
func (f runFixture) pinAll(t *testing.T, digest, commit, version string) {
	t.Helper()
	for _, env := range []string{"dev", "staging", "prod"} {
		f.pinOverlayRelease(t, env, digest, commit, version)
	}
}

// stubAllApps reports every one of the component's applications as synced and
// healthy at the revision the GitOps branch currently holds.
func (f runFixture) stubAllApps(t *testing.T) *stubArgoClient {
	t.Helper()
	head := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")
	stub := &stubArgoClient{get: map[string]*argocd.Application{}}
	for _, env := range []string{"dev", "staging", "prod"} {
		stub.get["orders-order-flow-order-extractor-"+env] = healthyApp(head)
	}
	stubArgo(t, stub)
	return stub
}

func (f runFixture) status(t *testing.T, opts StatusOptions) (stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	o := opts
	o.Stdout, o.Stderr = &out, &errBuf
	if err := Status(context.Background(), o); err != nil {
		t.Fatalf("Status() = %v", err)
	}
	return out.String(), errBuf.String()
}

// table is the rows alone, without the summary and notes beneath them — the
// summary repeats the digest, and a count over the whole output would find it.
func table(stdout string) string {
	rows, _, _ := strings.Cut(stdout, "\n\n")
	return rows
}

func statusJSON(t *testing.T, stdout string) StatusResult {
	t.Helper()
	var res StatusResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	return res
}

// The whole point of the design, visible in one line each: promotion copies
// digests rather than rebuilding, so the same digest in every row is what makes
// "production runs the bits staging tested" true.
func TestStatusReportsTheSameDigestInEveryEnvironment(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	for _, env := range []string{"dev", "staging", "prod"} {
		if !strings.Contains(out, env) {
			t.Errorf("output should have a row for %s:\n%s", env, out)
		}
	}
	if got := strings.Count(table(out), shortDigest(testDigest)); got != 3 {
		t.Errorf("the digest should appear once per environment, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "1.4.2") {
		t.Errorf("output should name the release:\n%s", out)
	}
	if !strings.Contains(out, "all 3 environments run") {
		t.Errorf("output should say the environments agree:\n%s", out)
	}
	if !strings.Contains(out, "Synced") || !strings.Contains(out, "Healthy") {
		t.Errorf("output should carry ArgoCD's sync and health:\n%s", out)
	}
}

// The failure this command exists to catch: something reached one environment
// without being promoted through the others.
func TestStatusReportsDivergingDigests(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "dev", testDigest, testReleaseCommit, "1.4.2")
	f.pinOverlayRelease(t, "staging", stagingDigest, testCommit, "1.4.1")
	f.pinOverlayRelease(t, "prod", stagingDigest, testCommit, "1.4.1")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if !strings.Contains(out, "do not all run the same bits") {
		t.Errorf("output should report the divergence:\n%s", out)
	}
	// Named in promotion order, and grouped: the two that agree together.
	if !strings.Contains(out, "staging and prod run") {
		t.Errorf("output should group the environments that agree:\n%s", out)
	}

	opts := f.statusOptions(&stdout, &stderr)
	opts.OutputFormat = OutputJSON
	jsonOut, _ := f.status(t, opts)
	if res := statusJSON(t, jsonOut); res.Consistent {
		t.Error("Consistent should be false when the environments disagree")
	}
}

// Alphabetical order would print dev, prod, staging and tell the wrong story
// about a pipeline.
func TestStatusOrdersRowsByPromotion(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	dev, staging, prod := strings.Index(out, "dev"), strings.Index(out, "staging"), strings.Index(out, "prod")
	if !(dev < staging && staging < prod) {
		t.Errorf("rows should read dev, staging, prod; got positions %d, %d, %d:\n%s", dev, staging, prod, out)
	}
}

// An environment deployed from a commit has no release annotation but still has
// something true to say. The @ prefix keeps it from reading as a version.
func TestStatusShowsTheSourceCommitWhenThereIsNoRelease(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlay(t, "dev", testDigest, testReleaseCommit)
	f.pinOverlayRelease(t, "staging", testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if !strings.Contains(out, "@"+testReleaseCommit[:7]) {
		t.Errorf("dev should show its source commit:\n%s", out)
	}
	if !strings.Contains(out, "1.4.2") {
		t.Errorf("staging should show its release version:\n%s", out)
	}
}

// The regression guard against reusing the strict readSnapshot: a promotion out
// of a tag-pinned overlay must be refused, but describing one must not be.
func TestStatusReportsATagPinWithoutFailing(t *testing.T) {
	f := newRunFixture(t)
	// Not "latest": the fixture's dev overlay already pins that, and rewriting
	// the same content commits nothing.
	f.pinTag(t, "dev", "v9")
	f.pinOverlayRelease(t, "staging", testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if !strings.Contains(out, ":v9") {
		t.Errorf("dev's tag should be visible in the digest column:\n%s", out)
	}
	if !strings.Contains(out, "rather than a digest") {
		t.Errorf("output should explain what a tag pin means:\n%s", out)
	}
	// staging is still described in full — the point of the table is the
	// environment that is fine next to the one that is not.
	if !strings.Contains(out, shortDigest(testDigest)) {
		t.Errorf("staging's digest should still be reported:\n%s", out)
	}
	// "nothing to compare" and "these disagree" are different findings. dev
	// pins a tag and prod pins nothing, so neither was compared with staging,
	// and reporting a disagreement between them would be inventing one.
	if !strings.Contains(out, "dev and prod pin no digest") {
		t.Errorf("environments with nothing to compare should be held apart from those that disagree:\n%s", out)
	}
}

// An environment pinning a tag and one pinning nothing are both incomparable,
// but for different reasons, and each note has to say which.
func TestStatusDistinguishesATagPinFromNoPinAtAll(t *testing.T) {
	f := newRunFixture(t)
	f.pinTag(t, "dev", "v9")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if !strings.Contains(out, `to the tag "v9" rather than a digest`) {
		t.Errorf("dev's note should name the tag:\n%s", out)
	}
	if !strings.Contains(out, "prod pins nothing for") {
		t.Errorf("prod's note should say it has never been deployed to:\n%s", out)
	}
	// Nothing is comparable, so the summary must not claim a disagreement.
	if strings.Contains(out, "do not all run the same bits") {
		t.Errorf("with nothing comparable there is no disagreement to report:\n%s", out)
	}
	if !strings.Contains(out, "nothing to compare") {
		t.Errorf("summary should say there is nothing to compare:\n%s", out)
	}
}

// An environment with no overlay is a fact to display, not an error.
func TestStatusReportsAnUnpinnedEnvironment(t *testing.T) {
	f := newRunFixture(t)
	f.pinOverlayRelease(t, "dev", testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if !strings.Contains(out, "pins nothing") {
		t.Errorf("output should say staging and prod have never been deployed to:\n%s", out)
	}
	if !strings.Contains(out, noValue) {
		t.Errorf("an empty cell should render as %q:\n%s", noValue, out)
	}
}

// The digest, release and age columns come from git and are still true with the
// cluster unreachable. Withholding them over the two columns that are not
// available would be withholding most of the answer over part of it.
func TestStatusStillReportsDigestsWhenArgoCDIsUnreachable(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")
	stubArgo(t, &stubArgoClient{getErr: argocd.ErrUnreachable})

	var stdout, stderr bytes.Buffer
	out, errOut := f.status(t, f.statusOptions(&stdout, &stderr))

	if got := strings.Count(table(out), shortDigest(testDigest)); got != 3 {
		t.Errorf("digests should print without ArgoCD, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "all 3 environments run") {
		t.Errorf("the consistency claim is a git fact and should still be made:\n%s", out)
	}
	if !strings.Contains(errOut, "warning:") {
		t.Errorf("stderr should warn that ArgoCD was not read:\n%s", errOut)
	}
	if strings.Contains(out, "Synced") {
		t.Errorf("sync status cannot be known and must not be invented:\n%s", out)
	}
}

// One application ArgoCD does not know must not blank out the others.
func TestStatusToleratesAnUnknownApplication(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")

	head := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")
	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-dev":     healthyApp(head),
		"orders-order-flow-order-extractor-staging": healthyApp(head),
		// prod's Application has not been created yet.
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	opts := f.statusOptions(&stdout, &stderr)
	opts.OutputFormat = OutputJSON
	out, errOut := f.status(t, opts)

	res := statusJSON(t, out)
	byEnv := map[string]EnvironmentStatus{}
	for _, e := range res.Environments {
		byEnv[e.Environment] = e
	}
	if byEnv["staging"].SyncStatus != argocd.SyncSynced {
		t.Errorf("staging should still be reported: %+v", byEnv["staging"])
	}
	if byEnv["prod"].SyncStatus != "" {
		t.Errorf("prod's sync status is unknown and must be empty: %+v", byEnv["prod"])
	}
	if byEnv["prod"].Pins[0].Digest != testDigest {
		t.Errorf("prod's digest comes from git and should be present: %+v", byEnv["prod"])
	}
	if !strings.Contains(errOut, "order-extractor-prod") {
		t.Errorf("stderr should name the application that could not be read:\n%s", errOut)
	}
}

// The resting state of an unspent manual gate: intent is committed, and nobody
// has applied it yet.
func TestStatusReportsAPendingChange(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")

	head := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")
	// prod holds an older revision, so its overlay commit has not reached the
	// cluster. HEAD~3 predates all three pins.
	older := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main~3")
	stub := &stubArgoClient{get: map[string]*argocd.Application{
		"orders-order-flow-order-extractor-dev":     healthyApp(head),
		"orders-order-flow-order-extractor-staging": healthyApp(head),
		"orders-order-flow-order-extractor-prod":    outOfSyncApp(older),
	}}
	stubArgo(t, stub)

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if !strings.Contains(out, "ArgoCD has not applied") {
		t.Errorf("output should report prod's unapplied change:\n%s", out)
	}
	if !strings.Contains(out, "manual sync gate") {
		t.Errorf("a manual environment's pending change is a gate, and should say so:\n%s", out)
	}
	if !strings.Contains(out, "intropy deploy diff order-extractor --env prod") {
		t.Errorf("output should point at the review command:\n%s", out)
	}
}

// The JSON carries what the table has no room for: every image, and the instant
// rather than a humanised age.
func TestStatusJSONCarriesEveryPinAndTheDeployTime(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	var stdout, stderr bytes.Buffer
	opts := f.statusOptions(&stdout, &stderr)
	opts.OutputFormat = OutputJSON
	out, _ := f.status(t, opts)

	res := statusJSON(t, out)
	if !res.Consistent {
		t.Error("Consistent should be true when every environment pins the same digest")
	}
	if len(res.Environments) != 3 {
		t.Fatalf("want 3 environments, got %d", len(res.Environments))
	}
	for _, e := range res.Environments {
		if len(e.Pins) != 1 || e.Pins[0].Digest != testDigest {
			t.Errorf("%s should carry the full digest: %+v", e.Environment, e.Pins)
		}
		if e.DeployedAt == nil || e.DeployedAt.IsZero() {
			t.Errorf("%s should be dated by its overlay commit", e.Environment)
		}
		if e.Revision == "" {
			t.Errorf("%s should name the commit that last changed its overlay", e.Environment)
		}
		if !e.Onboarded {
			t.Errorf("%s should be onboarded", e.Environment)
		}
	}
	if res.Environments[2].SyncPolicy != "manual" {
		t.Errorf("prod syncs manually in the fixture, got %q", res.Environments[2].SyncPolicy)
	}
	// The table's humanised age must not leak into the machine-readable form.
	if strings.Contains(out, `"age"`) {
		t.Errorf("JSON should carry an instant, not a rendered age:\n%s", out)
	}
}

// A read-only command must leave both repositories exactly as it found them,
// and must never ask ArgoCD to do anything.
func TestStatusWritesNothing(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")
	stub := f.stubAllApps(t)

	before := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main")
	var stdout, stderr bytes.Buffer
	f.status(t, f.statusOptions(&stdout, &stderr))

	if after := gittest.Run(t, f.gitopsOrigin, "rev-parse", "main"); after != before {
		t.Errorf("the GitOps branch moved: %s → %s", before, after)
	}
	if len(stub.synced) != 0 {
		t.Errorf("status must not sync anything, but asked for %v", stub.synced)
	}
}

// Truncating rather than rounding: a deployment reading as older than it is
// would be read as a change someone else already reviewed.
func TestHumanAge(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{time.Minute, "1m"},
		{47 * time.Minute, "47m"},
		{59*time.Minute + 59*time.Second, "59m"},
		{time.Hour, "1h"},
		{119 * time.Minute, "1h"},
		{2 * time.Hour, "2h"},
		{47 * time.Hour, "47h"},
		{48 * time.Hour, "2d"},
		{30 * 24 * time.Hour, "30d"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.in); got != tc.want {
			t.Errorf("humanAge(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The age column is computed against the clock, so freezing it pins the
// rendering rather than the git timestamps underneath.
func TestStatusRendersAgeFromTheOverlayCommit(t *testing.T) {
	f := newRunFixture(t)
	f.pinAll(t, testDigest, testReleaseCommit, "1.4.2")
	f.stubAllApps(t)

	original := now
	now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	t.Cleanup(func() { now = original })

	var stdout, stderr bytes.Buffer
	out, _ := f.status(t, f.statusOptions(&stdout, &stderr))

	if got := strings.Count(out, "3h"); got != 3 {
		t.Errorf("every row should be three hours old, got %d:\n%s", got, out)
	}
}
