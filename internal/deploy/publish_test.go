package deploy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// publishOpts builds options with the delays removed: the backoff is real
// behaviour but waiting for it in a test buys nothing.
func publishOpts(t *testing.T, repo *repoFixture, plan *Plan) PublishOptions {
	t.Helper()
	return PublishOptions{
		Repository: repo.repo,
		Plan:       plan,
		CliVersion: "intropy-cli/test",
		Sleep:      func(time.Duration) {},
	}
}

func TestPublishCommitsAndPushes(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)

	revision, err := Publish(context.Background(), publishOpts(t, f, plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(revision) != 40 {
		t.Errorf("revision = %q, want a full sha", revision)
	}

	// The commit must be on the origin, not merely local.
	if got := gittest.Run(t, f.origin, "rev-parse", "main"); got != revision {
		t.Errorf("origin/main = %q, want the pushed revision %q", got, revision)
	}

	// And it must contain only the overlay file.
	files := gittest.Run(t, f.origin, "show", "--name-only", "--format=", revision)
	if strings.Count(strings.TrimSpace(files), "\n") != 0 {
		t.Errorf("commit should touch exactly one file, got:\n%s", files)
	}
	if !strings.HasSuffix(strings.TrimSpace(files), "overlays/dev/kustomization.yaml") {
		t.Errorf("commit touched the wrong file: %s", files)
	}
}

func TestPublishWritesParseableTrailers(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)

	revision, err := Publish(context.Background(), publishOpts(t, f, plan))
	if err != nil {
		t.Fatal(err)
	}

	subject := gittest.Run(t, f.origin, "log", "-1", "--format=%s", revision)
	if !strings.Contains(subject, "deploy(order-extractor)") || !strings.Contains(subject, "dev") {
		t.Errorf("subject = %q", subject)
	}
	// The full digest belongs in the trailer, not the subject.
	if strings.Contains(subject, testDigest) {
		t.Errorf("subject should abbreviate the digest, got %q", subject)
	}

	// git must recognise the block as trailers, or `deploy history` cannot read
	// them back — that is what makes this a format rather than prose.
	raw := gittest.Run(t, f.origin, "log", "-1", "--format=%(trailers:only=true)", revision)
	trailers := map[string]string{}
	for line := range strings.SplitSeq(raw, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			trailers[k] = strings.TrimSpace(v)
		}
	}

	want := map[string]string{
		TrailerComponent:    "order-extractor",
		TrailerDomain:       "orders",
		TrailerSystem:       "order-flow",
		TrailerEnvironment:  "dev",
		TrailerDigest:       testDigest,
		TrailerSourceCommit: testCommit,
		TrailerCli:          "intropy-cli/test",
	}
	for k, v := range want {
		if trailers[k] != v {
			t.Errorf("trailer %s = %q, want %q (all: %v)", k, trailers[k], v, trailers)
		}
	}
	if trailers[TrailerImage] == "" {
		t.Errorf("trailer %s missing (all: %v)", TrailerImage, trailers)
	}
}

// Someone else pushed between our fetch and our push. A one-line change to one
// file replays cleanly, so the retry should succeed and return the rewritten
// sha — not the sha of the commit we first created.
func TestPublishRebasesAndRetriesOnRejection(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)

	// Land an unrelated commit on the origin after our checkout was refreshed.
	gittest.Commit(t, f.origin, "unrelated.txt", "someone else's work\n", "unrelated change")

	var stderr strings.Builder
	opts := publishOpts(t, f, plan)
	opts.Stderr = &stderr

	revision, err := Publish(context.Background(), opts)
	if err != nil {
		t.Fatalf("the retry should have succeeded: %v", err)
	}
	if got := gittest.Run(t, f.origin, "rev-parse", "main"); got != revision {
		t.Errorf("origin/main = %q, want %q", got, revision)
	}
	if !strings.Contains(stderr.String(), "rebasing") {
		t.Errorf("stderr should report the retry:\n%s", stderr.String())
	}
	// Their commit must survive ours.
	log := gittest.Run(t, f.origin, "log", "--format=%s", "main")
	if !strings.Contains(log, "unrelated change") {
		t.Errorf("the concurrent commit was lost:\n%s", log)
	}
}

// The returned sha must be the post-rebase one. The ArgoCD revision gate
// compares against it, and using the pre-rebase sha would wait for a commit
// that no longer exists on the branch.
func TestPublishReturnsPostRebaseRevision(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)
	gittest.Commit(t, f.origin, "unrelated.txt", "x\n", "unrelated change")

	revision, err := Publish(context.Background(), publishOpts(t, f, plan))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gittest.Run(t, f.origin, "log", "--format=%H", "main"), revision) {
		t.Errorf("revision %q is not on origin/main", revision)
	}
	if gittest.Run(t, f.origin, "rev-parse", "main") != revision {
		t.Error("revision should be the tip of origin/main after the rebase")
	}
}

// Two deploys of the same component to the same environment at the same moment
// edit the same line. Auto-resolving would silently pick a winner and discard a
// deployment someone believes succeeded, so this must fail loudly and push
// nothing.
func TestPublishFailsLoudlyOnConflict(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)

	// A competing pin of the same overlay, landing first.
	rival := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	overlay := "domains/orders/order-flow/order-extractor/overlays/dev/kustomization.yaml"
	gittest.WriteFile(t, f.origin+"/"+overlay,
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: integrations\nresources:\n  - ../../base\nimages:\n  - name: "+f.image+"\n    digest: "+rival+"\ncommonAnnotations:\n  deploy.internal/source-commit: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	gittest.Run(t, f.origin, "add", "-A")
	gittest.Run(t, f.origin, "commit", "--quiet", "-m", "rival deploy")
	before := gittest.Run(t, f.origin, "rev-parse", "main")

	_, err := Publish(context.Background(), publishOpts(t, f, plan))
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	for _, want := range []string{"same moment", "Nothing was pushed", "Re-run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}

	// Nothing pushed, and no rebase left in progress to poison the next run.
	if got := gittest.Run(t, f.origin, "rev-parse", "main"); got != before {
		t.Errorf("origin/main moved to %q; nothing should have been pushed", got)
	}
	if status := gittest.Run(t, f.repo.Root, "status", "--porcelain=2", "--branch"); strings.Contains(status, "rebase") {
		t.Errorf("a rebase was left in progress:\n%s", status)
	}
}

// Retrying an authentication or network failure cannot help, so it must surface
// immediately rather than burn five attempts.
func TestPublishDoesNotRetryNonRejection(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)

	// Point the remote at a path that does not exist.
	gittest.Run(t, f.repo.Root, "remote", "set-url", "origin", f.origin+"-gone")

	var stderr strings.Builder
	opts := publishOpts(t, f, plan)
	opts.Stderr = &stderr

	if _, err := Publish(context.Background(), opts); err == nil {
		t.Fatal("expected a push failure")
	}
	if strings.Contains(stderr.String(), "rebasing") {
		t.Errorf("a non-rejection failure should not be retried:\n%s", stderr.String())
	}
}

// The retry is bounded. With one attempt allowed, a rejection must surface
// rather than loop — proving the bound is enforced at all.
func TestPublishGivesUpAfterMaxAttempts(t *testing.T) {
	f := newRepoFixture(t)
	plan := f.buildPlan(t, testDigest)

	// Put the origin ahead so the first push is rejected.
	gittest.Commit(t, f.origin, "unrelated.txt", "x\n", "unrelated change")
	before := gittest.Run(t, f.origin, "rev-parse", "main")

	opts := publishOpts(t, f, plan)
	opts.MaxAttempts = 1

	_, err := Publish(context.Background(), opts)
	if err == nil {
		t.Fatal("expected the retry loop to give up")
	}
	if !strings.Contains(err.Error(), "1 push attempt:") {
		t.Errorf("error %q should report how many attempts were made, in agreeing number", err)
	}
	if got := gittest.Run(t, f.origin, "rev-parse", "main"); got != before {
		t.Errorf("origin/main moved to %q; nothing should have been pushed", got)
	}
}

func TestCommitSubject(t *testing.T) {
	plan := &Plan{
		Coordinate:  coordFixture,
		Environment: "dev",
		Source:      SourceState{Commit: testCommit},
		Pins:        []Pin{{Image: "r/i", Digest: testDigest}},
	}
	subject := commitSubject(plan)
	if !strings.HasPrefix(subject, "deploy(order-extractor): dev → sha256:") {
		t.Errorf("subject = %q", subject)
	}
	// Abbreviated, so a git log stays readable.
	if len(subject) > 80 {
		t.Errorf("subject is %d chars, too long for a log: %q", len(subject), subject)
	}

	plan.Pins = append(plan.Pins, Pin{Image: "r/j", Digest: testDigest})
	if multi := commitSubject(plan); !strings.Contains(multi, "2 images") {
		t.Errorf("multi-image subject = %q, want an image count", multi)
	}
}

func TestShortDigest(t *testing.T) {
	cases := map[string]string{
		testDigest:      "sha256:abc123abc123",
		"sha256:abc":    "sha256:abc",
		"notadigest":    "notadigest",
		"sha512:abcdef": "sha512:abcdef",
	}
	for in, want := range cases {
		if got := shortDigest(in); got != want {
			t.Errorf("shortDigest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJitteredBackoffGrowsAndVaries(t *testing.T) {
	// Later attempts must wait longer, or a burst of collisions keeps colliding.
	if jitteredBackoff(1) >= jitteredBackoff(4) {
		t.Error("backoff should grow with the attempt number")
	}
	// And two callers must not wait exactly the same time, or they retry in
	// lockstep and collide again.
	seen := map[time.Duration]bool{}
	for range 20 {
		seen[jitteredBackoff(3)] = true
	}
	if len(seen) == 1 {
		t.Error("backoff should be jittered, not fixed")
	}
}
