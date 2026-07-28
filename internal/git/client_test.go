package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

func testClient(dir string) Client {
	return Client{Runner: command.ExecRunner{}, Dir: dir}
}

func TestGitHEADReturnsFullSha(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	sha, err := testClient(dir).HEAD(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Full, not abbreviated: an abbreviated sha is ambiguous both as a
	// registry tag and as an argument to merge-base.
	if len(sha) != 40 {
		t.Errorf("HEAD() = %q (%d chars), want a full 40-character sha", sha, len(sha))
	}
}

func TestGitStatusScopesToPaths(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	gittest.WriteFile(t, filepath.Join(dir, "component", "app.cs"), "// changed\n")
	gittest.WriteFile(t, filepath.Join(dir, "unrelated", "notes.txt"), "dirty\n")
	g := testClient(dir)
	ctx := context.Background()

	all, err := g.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("unscoped Status() = %v, want 2 entries", all)
	}

	// The whole point of scoping: an unrelated dirty file in a monorepo must
	// not block a deploy of this component.
	scoped, err := g.Status(ctx, "component")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || !strings.Contains(scoped[0], "component/app.cs") {
		t.Errorf("scoped Status() = %v, want only component/app.cs", scoped)
	}

	clean, err := g.Status(ctx, "unrelated/absent.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(clean) != 0 {
		t.Errorf("Status() on an untouched path = %v, want none", clean)
	}
}

// Which commit put a file in its current state — the question a manual sync has
// to answer to know which revision was reviewed.
func TestGitLastCommitIgnoresLaterUnrelatedCommits(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	gittest.Commit(t, dir, "overlays/prod/kustomization.yaml", "digest: one\n", "pin prod")
	want, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Later commits that do not touch the overlay must not shadow it.
	gittest.Commit(t, dir, "unrelated.txt", "x\n", "unrelated change")

	got, found, err := g.LastCommit(ctx, "HEAD", "overlays/prod/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("LastCommit() found nothing for a path that exists")
	}
	if got != want {
		t.Errorf("LastCommit() = %q, want the commit that pinned prod %q", got, want)
	}
}

// A component onboarded but never deployed: the overlay path has no history of
// its own. Reporting that as "not found" rather than an error is what lets sync
// say "there is nothing pending" instead of failing obscurely.
func TestGitLastCommitOfAnUntouchedPath(t *testing.T) {
	dir := gittest.NewRepo(t, "main")

	got, found, err := testClient(dir).LastCommit(context.Background(), "HEAD", "never/existed.yaml")
	if err != nil {
		t.Fatalf("an untouched path should not be an error: %v", err)
	}
	if found || got != "" {
		t.Errorf("LastCommit() = %q, %v; want no commit", got, found)
	}
}

// Dating a deployment: the overlay changed when the commit that changed it
// landed, and that timestamp is the only source of a deployment's age — ArgoCD
// reports none.
func TestGitLastCommitAtReportsWhenTheCommitLanded(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	before := time.Now().Add(-time.Second).Truncate(time.Second)
	gittest.Commit(t, dir, "overlays/prod/kustomization.yaml", "digest: one\n", "pin prod")
	after := time.Now().Add(time.Second)

	sha, at, found, err := g.LastCommitAt(ctx, "HEAD", "overlays/prod/kustomization.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("LastCommitAt() found nothing for a path that exists")
	}
	if want := gittest.HEAD(t, dir); sha != want {
		t.Errorf("LastCommitAt() sha = %q, want %q", sha, want)
	}
	if at.Before(before) || at.After(after) {
		t.Errorf("LastCommitAt() at = %s, want between %s and %s", at, before, after)
	}
}

// The same "nothing pending" case as LastCommit: an onboarded but never
// deployed overlay has no history, which is a fact rather than a failure.
func TestGitLastCommitAtOfAnUntouchedPath(t *testing.T) {
	dir := gittest.NewRepo(t, "main")

	sha, at, found, err := testClient(dir).LastCommitAt(context.Background(), "HEAD", "never/existed.yaml")
	if err != nil {
		t.Fatalf("an untouched path should not be an error: %v", err)
	}
	if found || sha != "" || !at.IsZero() {
		t.Errorf("LastCommitAt() = %q, %s, %v; want no commit", sha, at, found)
	}
}

func TestGitIsAncestor(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	first, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gittest.WriteFile(t, filepath.Join(dir, "second.txt"), "x\n")
	gittest.Run(t, dir, "add", ".")
	gittest.Run(t, dir, "commit", "--quiet", "-m", "second")
	second, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if ok, err := g.IsAncestor(ctx, first, second); err != nil || !ok {
		t.Errorf("IsAncestor(first, second) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := g.IsAncestor(ctx, second, first); err != nil || ok {
		t.Errorf("IsAncestor(second, first) = %v, %v; want false, nil", ok, err)
	}
	// git treats a commit as its own ancestor, and the ArgoCD revision gate
	// relies on that: the common case is revision == the commit we pushed.
	if ok, err := g.IsAncestor(ctx, first, first); err != nil || !ok {
		t.Errorf("IsAncestor(first, first) = %v, %v; want true, nil", ok, err)
	}
}

// A bad revision must not be reported as a clean "false". git exits 1 for "not
// an ancestor" and 128 for a bad revision; conflating them would let a typo
// masquerade as a legitimate negative answer.
func TestGitIsAncestorBadRevisionIsAnError(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	_, err := testClient(dir).IsAncestor(context.Background(), "not-a-commit", "HEAD")
	if err == nil {
		t.Fatal("IsAncestor with a bad revision should error, not return false")
	}
}

func TestGitDefaultBranchFromSymbolicRef(t *testing.T) {
	for _, branch := range []string{"main", "master", "trunk"} {
		t.Run(branch, func(t *testing.T) {
			origin := gittest.NewRepo(t, branch)
			clone := filepath.Join(t.TempDir(), "clone")
			ctx := context.Background()
			if err := Clone(ctx, command.ExecRunner{}, origin, clone); err != nil {
				t.Fatal(err)
			}
			got, err := testClient(clone).DefaultBranch(ctx, "origin")
			if err != nil {
				t.Fatal(err)
			}
			if got != branch {
				t.Errorf("DefaultBranch() = %q, want %q", got, branch)
			}
		})
	}
}

// A clone made without origin/HEAD has to fall back to asking the remote;
// hardcoding "main" would break any repository still on master.
func TestGitDefaultBranchFallsBackToLsRemote(t *testing.T) {
	origin := gittest.NewRepo(t, "master")
	clone := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if err := Clone(ctx, command.ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "remote", "set-head", "origin", "--delete")

	got, err := testClient(clone).DefaultBranch(ctx, "origin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "master" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "master")
	}
}

func TestGitCheckoutPathsRequiresAPath(t *testing.T) {
	// An unscoped revert would discard everything in the worktree, so refuse
	// rather than let a caller omit the path by accident.
	if err := testClient(t.TempDir()).CheckoutPaths(context.Background()); err == nil {
		t.Fatal("CheckoutPaths() with no paths should fail")
	}
}

func TestGitCheckoutPathsDiscardsOnlyGivenPaths(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	gittest.WriteFile(t, filepath.Join(dir, "keep.txt"), "keep\n")
	gittest.Run(t, dir, "add", ".")
	gittest.Run(t, dir, "commit", "--quiet", "-m", "add keep")

	gittest.WriteFile(t, filepath.Join(dir, "README.md"), "reverted\n")
	gittest.WriteFile(t, filepath.Join(dir, "keep.txt"), "modified\n")

	if err := testClient(dir).CheckoutPaths(context.Background(), "README.md"); err != nil {
		t.Fatal(err)
	}

	if got := gittest.ReadFile(t, filepath.Join(dir, "README.md")); got != "hello\n" {
		t.Errorf("README.md = %q, want it reverted", got)
	}
	if got := gittest.ReadFile(t, filepath.Join(dir, "keep.txt")); got != "modified\n" {
		t.Errorf("keep.txt = %q, want it untouched", got)
	}
}

func TestGitErrorsAreWrappedWithContext(t *testing.T) {
	g := Client{Runner: failingRunner{}, Dir: "/nowhere"}
	if _, err := g.HEAD(context.Background()); err == nil || !strings.Contains(err.Error(), "resolve HEAD") {
		t.Errorf("HEAD() error = %v, want it wrapped with context", err)
	}
}

// failingRunner fails every command, for checking error wrapping without a real
// repository.
type failingRunner struct{}

func (failingRunner) Run(context.Context, string, string, ...string) ([]byte, []byte, error) {
	return nil, nil, errors.New("boom")
}

func TestCommitStagesNothingImplicitly(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	gittest.WriteFile(t, filepath.Join(dir, "staged.txt"), "in\n")
	gittest.WriteFile(t, filepath.Join(dir, "unstaged.txt"), "out\n")
	if err := g.Add(ctx, "staged.txt"); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(ctx, "add staged"); err != nil {
		t.Fatal(err)
	}

	// There is no -a, so the caller decides exactly what the commit contains.
	files := gittest.Run(t, dir, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "unstaged.txt") {
		t.Errorf("commit should contain only staged files, got:\n%s", files)
	}
	if !strings.Contains(files, "staged.txt") {
		t.Errorf("commit is missing the staged file:\n%s", files)
	}
}

// Each message becomes a paragraph, which is how a subject plus a trailer block
// is produced without a temporary file.
func TestCommitMessageParagraphsBecomeTrailers(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	g := testClient(dir)
	ctx := context.Background()

	gittest.WriteFile(t, filepath.Join(dir, "f.txt"), "x\n")
	if err := g.Add(ctx, "f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(ctx, "subject line", "Key-One: first\nKey-Two: second"); err != nil {
		t.Fatal(err)
	}

	if got := gittest.Run(t, dir, "log", "-1", "--format=%s"); got != "subject line" {
		t.Errorf("subject = %q", got)
	}
	trailers, err := g.Trailers(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(trailers) != 2 {
		t.Fatalf("Trailers() = %+v, want 2", trailers)
	}
	if trailers[0].Key != "Key-One" || trailers[0].Value != "first" {
		t.Errorf("trailers[0] = %+v", trailers[0])
	}
	if trailers[1].String() != "Key-Two: second" {
		t.Errorf("trailers[1].String() = %q", trailers[1].String())
	}
}

func TestCommitRequiresASubject(t *testing.T) {
	if err := testClient(t.TempDir()).Commit(context.Background()); err == nil {
		t.Fatal("Commit() with no message should fail")
	}
}

func TestPushAndRejection(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	clone := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if err := Clone(ctx, command.ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "config", "user.email", "t@e.com")
	gittest.Run(t, clone, "config", "user.name", "T")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	g := testClient(clone)

	gittest.Commit(t, clone, "ours.txt", "ours\n", "our change")
	if err := g.Push(ctx, "origin", "HEAD:main"); err != nil {
		t.Fatalf("Push() = %v, want nil", err)
	}

	// Now diverge: the origin moves ahead and our next push must be rejected as
	// a rejection specifically, so the caller knows a rebase can fix it.
	gittest.Commit(t, origin, "theirs.txt", "theirs\n", "their change")
	gittest.Commit(t, clone, "ours2.txt", "ours\n", "our second change")

	err := g.Push(ctx, "origin", "HEAD:main")
	if err == nil {
		t.Fatal("expected the push to be rejected")
	}
	if _, ok := errors.AsType[*PushRejectedError](err); !ok {
		t.Fatalf("error %v should be *PushRejectedError", err)
	}
}

// An unreachable remote is not a rejection: retrying it cannot help, so it must
// not be reported as something a rebase would fix.
func TestPushUnreachableRemoteIsNotARejection(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	gittest.Run(t, dir, "remote", "add", "origin", filepath.Join(t.TempDir(), "absent"))

	err := testClient(dir).Push(context.Background(), "origin", "HEAD:main")
	if err == nil {
		t.Fatal("expected a push failure")
	}
	if _, ok := errors.AsType[*PushRejectedError](err); ok {
		t.Errorf("an unreachable remote should not be a *PushRejectedError: %v", err)
	}
}

func TestRebaseReplaysCleanly(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	clone := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if err := Clone(ctx, command.ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "config", "user.email", "t@e.com")
	gittest.Run(t, clone, "config", "user.name", "T")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	g := testClient(clone)

	// Different files, so the replay cannot conflict.
	gittest.Commit(t, origin, "theirs.txt", "theirs\n", "their change")
	gittest.Commit(t, clone, "ours.txt", "ours\n", "our change")

	if err := g.Fetch(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.Rebase(ctx, "origin/main"); err != nil {
		t.Fatalf("Rebase() = %v, want nil", err)
	}
	log := gittest.Run(t, clone, "log", "--format=%s")
	if !strings.Contains(log, "our change") || !strings.Contains(log, "their change") {
		t.Errorf("both commits should survive the rebase:\n%s", log)
	}
}

func TestRebaseConflictIsTypedAndAbortable(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	clone := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if err := Clone(ctx, command.ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "config", "user.email", "t@e.com")
	gittest.Run(t, clone, "config", "user.name", "T")
	gittest.Run(t, clone, "config", "commit.gpgsign", "false")
	g := testClient(clone)

	// Both edit the same line of the same file.
	gittest.Commit(t, origin, "shared.txt", "theirs\n", "their edit")
	gittest.Commit(t, clone, "shared.txt", "ours\n", "our edit")

	if err := g.Fetch(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	err := g.Rebase(ctx, "origin/main")
	if err == nil {
		t.Fatal("expected a rebase conflict")
	}
	if _, ok := errors.AsType[*RebaseConflictError](err); !ok {
		t.Fatalf("error %v should be *RebaseConflictError", err)
	}

	// The rebase is left in progress deliberately — whether a conflict is
	// recoverable is the caller's policy — so aborting must restore the state.
	if err := g.RebaseAbort(ctx); err != nil {
		t.Fatal(err)
	}
	if status := gittest.Run(t, clone, "status", "--porcelain=2", "--branch"); strings.Contains(status, "rebase") {
		t.Errorf("abort should have ended the rebase:\n%s", status)
	}
	if got := gittest.ReadFile(t, filepath.Join(clone, "shared.txt")); got != "ours\n" {
		t.Errorf("shared.txt = %q, want our version restored", got)
	}
}

func TestTrailersOnCommitWithout(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	trailers, err := testClient(dir).Trailers(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(trailers) != 0 {
		t.Errorf("Trailers() = %+v, want none", trailers)
	}
}
