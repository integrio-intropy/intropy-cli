package deploy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo creates a git repository with one commit on the given branch and
// returns its path. Real git, not a fake: clone, fetch, reset and ancestry are
// exactly the operations where a mock would encode our assumptions rather than
// git's behaviour.
func newRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet", "--initial-branch="+branch)
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	// Commit signing would prompt or fail in CI.
	runGit(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "--quiet", "-m", "initial")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testGit(dir string) Git {
	return Git{Runner: ExecRunner{}, Dir: dir}
}

func TestGitHEADReturnsFullSha(t *testing.T) {
	dir := newRepo(t, "main")
	sha, err := testGit(dir).HEAD(context.Background())
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
	dir := newRepo(t, "main")
	writeFile(t, filepath.Join(dir, "component", "app.cs"), "// changed\n")
	writeFile(t, filepath.Join(dir, "unrelated", "notes.txt"), "dirty\n")
	g := testGit(dir)
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

func TestGitIsAncestor(t *testing.T) {
	dir := newRepo(t, "main")
	g := testGit(dir)
	ctx := context.Background()

	first, err := g.HEAD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "second.txt"), "x\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "--quiet", "-m", "second")
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
	dir := newRepo(t, "main")
	_, err := testGit(dir).IsAncestor(context.Background(), "not-a-commit", "HEAD")
	if err == nil {
		t.Fatal("IsAncestor with a bad revision should error, not return false")
	}
}

func TestGitDefaultBranchFromSymbolicRef(t *testing.T) {
	for _, branch := range []string{"main", "master", "trunk"} {
		t.Run(branch, func(t *testing.T) {
			origin := newRepo(t, branch)
			clone := filepath.Join(t.TempDir(), "clone")
			ctx := context.Background()
			if err := Clone(ctx, ExecRunner{}, origin, clone); err != nil {
				t.Fatal(err)
			}
			got, err := testGit(clone).DefaultBranch(ctx, "origin")
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
	origin := newRepo(t, "master")
	clone := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if err := Clone(ctx, ExecRunner{}, origin, clone); err != nil {
		t.Fatal(err)
	}
	runGit(t, clone, "remote", "set-head", "origin", "--delete")

	got, err := testGit(clone).DefaultBranch(ctx, "origin")
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
	if err := testGit(t.TempDir()).CheckoutPaths(context.Background()); err == nil {
		t.Fatal("CheckoutPaths() with no paths should fail")
	}
}

func TestGitCheckoutPathsDiscardsOnlyGivenPaths(t *testing.T) {
	dir := newRepo(t, "main")
	writeFile(t, filepath.Join(dir, "keep.txt"), "keep\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "--quiet", "-m", "add keep")

	writeFile(t, filepath.Join(dir, "README.md"), "reverted\n")
	writeFile(t, filepath.Join(dir, "keep.txt"), "modified\n")

	if err := testGit(dir).CheckoutPaths(context.Background(), "README.md"); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dir, "README.md")); got != "hello\n" {
		t.Errorf("README.md = %q, want it reverted", got)
	}
	if got := readFile(t, filepath.Join(dir, "keep.txt")); got != "modified\n" {
		t.Errorf("keep.txt = %q, want it untouched", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGitErrorsAreWrappedWithContext(t *testing.T) {
	g := Git{Runner: &fakeRunner{def: fakeResult{err: errors.New("boom")}}, Dir: "/nowhere"}
	if _, err := g.HEAD(context.Background()); err == nil || !strings.Contains(err.Error(), "resolve HEAD") {
		t.Errorf("HEAD() error = %v, want it wrapped with context", err)
	}
}
