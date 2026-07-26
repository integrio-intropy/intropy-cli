package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openOpts builds options pointing the cache at a temporary directory.
//
// The cache is injected rather than redirected through HOME: on macOS,
// overriding HOME so os.UserCacheDir follows it makes every git invocation stall
// for five seconds on a home directory that does not match the passwd entry,
// which turned this file into a 90-second test run.
func openOpts(t *testing.T, url string) WorktreeOptions {
	t.Helper()
	return WorktreeOptions{URL: url, Runner: ExecRunner{}, CacheRoot: t.TempDir()}
}

func TestOpenWorktreeClonesThenReuses(t *testing.T) {
	origin := newRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "main" {
		t.Errorf("Branch = %q, want main", wt.Branch)
	}
	if _, err := os.Stat(filepath.Join(wt.Root, "README.md")); err != nil {
		t.Errorf("clone is missing README.md: %v", err)
	}
	first := wt.Root
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	// The cache is the point: a second run must land in the same directory
	// rather than pay for another clone.
	again, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Root != first {
		t.Errorf("second open used %q, want the cached %q", again.Root, first)
	}
}

func TestOpenWorktreePicksUpNewCommits(t *testing.T) {
	origin := newRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(origin, "new.txt"), "added upstream\n")
	runGit(t, origin, "add", ".")
	runGit(t, origin, "commit", "--quiet", "-m", "upstream change")

	wt, err = OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()
	if _, err := os.Stat(filepath.Join(wt.Root, "new.txt")); err != nil {
		t.Errorf("refresh did not pick up the upstream commit: %v", err)
	}
}

// A run killed part-way through an edit leaves the cached checkout dirty. That
// edit must never reach the next run's commit, so refresh hard-resets tracked
// files and removes untracked ones.
func TestOpenWorktreeDiscardsDebrisFromAPreviousRun(t *testing.T) {
	origin := newRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	writeFile(t, filepath.Join(root, "README.md"), "half-finished edit\n")
	writeFile(t, filepath.Join(root, "stray.yaml"), "left behind\n")
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	wt, err = OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	if got := readFile(t, filepath.Join(root, "README.md")); got != "hello\n" {
		t.Errorf("README.md = %q, want the modification discarded", got)
	}
	if _, err := os.Stat(filepath.Join(root, "stray.yaml")); !os.IsNotExist(err) {
		t.Error("untracked debris should have been cleaned")
	}
}

// An interrupted clone leaves a directory with no .git, which git clone then
// refuses to write into. Without clearing it, the cache would be permanently
// poisoned and every later run would fail.
func TestOpenWorktreeRecoversFromInterruptedClone(t *testing.T) {
	origin := newRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	writeFile(t, filepath.Join(worktreeDir(opts.CacheRoot, origin), "partial.txt"), "debris\n")

	wt, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatalf("should have recovered from a partial clone: %v", err)
	}
	defer wt.Close()
	if _, err := os.Stat(filepath.Join(wt.Root, "README.md")); err != nil {
		t.Errorf("recovery did not produce a usable clone: %v", err)
	}
}

// Two concurrent deploys sharing one checkout would interleave edits, so the
// second must be told to wait rather than silently queue or proceed.
func TestOpenWorktreeIsExclusive(t *testing.T) {
	origin := newRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	first, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	_, err = OpenWorktree(ctx, opts)
	if err == nil {
		t.Fatal("a second concurrent open should fail")
	}
	if !strings.Contains(err.Error(), "already using") {
		t.Errorf("error %q should explain another deploy holds the checkout", err)
	}

	// Closing the first releases the lock for the next run.
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := OpenWorktree(ctx, opts)
	if err != nil {
		t.Fatalf("open after Close should succeed: %v", err)
	}
	third.Close()
}

func TestOpenWorktreeRequiresURL(t *testing.T) {
	if _, err := OpenWorktree(context.Background(), WorktreeOptions{Runner: ExecRunner{}, CacheRoot: t.TempDir()}); err == nil {
		t.Fatal("expected an error for an empty URL")
	}
}

// The same repository is reachable by several URLs, and each must get its own
// checkout rather than fighting over one.
func TestWorktreeDirIsStablePerURL(t *testing.T) {
	root := t.TempDir()
	a := worktreeDir(root, "git@gitlab.com:acme/gitops.git")
	again := worktreeDir(root, "git@gitlab.com:acme/gitops.git")
	b := worktreeDir(root, "https://gitlab.com/acme/gitops.git")

	if a != again {
		t.Errorf("worktreeDir is not stable: %q vs %q", a, again)
	}
	if a == b {
		t.Error("different URLs should map to different directories")
	}
	// The path must be usable and readable, not just unique.
	if strings.ContainsAny(filepath.Base(a), "@:/") {
		t.Errorf("cache directory name %q should not contain URL punctuation", filepath.Base(a))
	}
}

func TestWorktreeCloseIsIdempotent(t *testing.T) {
	origin := newRepo(t, "main")
	opts := openOpts(t, origin)
	wt, err := OpenWorktree(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil", err)
	}
}
