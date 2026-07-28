package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// openOpts builds options pointing the cache at a temporary directory.
//
// The cache is injected rather than redirected through HOME: on macOS,
// overriding HOME so os.UserCacheDir follows it makes every git invocation stall
// for five seconds on a home directory that does not match the passwd entry,
// which turned this file into a 90-second test run.
func openOpts(t *testing.T, url string) Options {
	t.Helper()
	return Options{URL: url, Runner: command.ExecRunner{}, CacheRoot: t.TempDir()}
}

func TestOpenClonesThenReuses(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
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
	again, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Root != first {
		t.Errorf("second open used %q, want the cached %q", again.Root, first)
	}
}

func TestOpenPicksUpNewCommits(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	gittest.WriteFile(t, filepath.Join(origin, "new.txt"), "added upstream\n")
	gittest.Run(t, origin, "add", ".")
	gittest.Run(t, origin, "commit", "--quiet", "-m", "upstream change")

	wt, err = Open(ctx, opts)
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
func TestOpenDiscardsDebrisFromAPreviousRun(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	gittest.WriteFile(t, filepath.Join(root, "README.md"), "half-finished edit\n")
	gittest.WriteFile(t, filepath.Join(root, "stray.yaml"), "left behind\n")
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	wt, err = Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	if got := gittest.ReadFile(t, filepath.Join(root, "README.md")); got != "hello\n" {
		t.Errorf("README.md = %q, want the modification discarded", got)
	}
	if _, err := os.Stat(filepath.Join(root, "stray.yaml")); !os.IsNotExist(err) {
		t.Error("untracked debris should have been cleaned")
	}
}

// An interrupted clone leaves a directory with no .git, which git clone then
// refuses to write into. Without clearing it, the cache would be permanently
// poisoned and every later run would fail.
func TestOpenRecoversFromInterruptedClone(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	gittest.WriteFile(t, filepath.Join(CheckoutDir(opts.CacheRoot, origin), "partial.txt"), "debris\n")

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatalf("should have recovered from a partial clone: %v", err)
	}
	defer wt.Close()
	if _, err := os.Stat(filepath.Join(wt.Root, "README.md")); err != nil {
		t.Errorf("recovery did not produce a usable clone: %v", err)
	}
}

// The cache directory is named after the URL it was created for, which says
// nothing about where its git config points now. If a stale, corrupted or edited
// origin were trusted, the fetch that decides what is deployed and the push that
// publishes it would both go to a repository the user never named.
func TestOpenRefusesToTrustACachedOriginThatMoved(t *testing.T) {
	ours := gittest.NewRepo(t, "main")
	theirs := gittest.NewRepo(t, "main")
	gittest.Commit(t, theirs, "theirs.txt", "someone else's repository\n", "not ours")

	opts := openOpts(t, ours)
	var notes strings.Builder
	opts.Stderr = &notes
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	gittest.Run(t, root, "remote", "set-url", RemoteName, theirs)

	wt, err = Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	have, ok, err := wt.Git.RemoteURL(ctx, RemoteName)
	if err != nil || !ok {
		t.Fatalf("RemoteURL = %q, %v, %v", have, ok, err)
	}
	if !SameRepository(have, ours) {
		t.Errorf("origin = %q, want the configured %q", have, ours)
	}
	if _, err := os.Stat(filepath.Join(wt.Root, "theirs.txt")); err == nil {
		t.Error("the checkout still holds the other repository's content")
	}
	if !strings.Contains(notes.String(), "re-cloning") {
		t.Errorf("the user was not told the cache was replaced: %q", notes.String())
	}
}

// remote.origin.url is where a fetch comes from; a pushurl is where a push goes.
// This CLI never sets one, so in a directory it owns that key is debris or
// tampering either way — and left alone it would redirect a production deployment.
func TestOpenRemovesAPushurlFromTheCache(t *testing.T) {
	ours := gittest.NewRepo(t, "main")
	theirs := gittest.NewRepo(t, "main")

	opts := openOpts(t, ours)
	var notes strings.Builder
	opts.Stderr = &notes
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	gittest.Run(t, root, "remote", "set-url", "--push", RemoteName, theirs)

	wt, err = Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	// Read through the client rather than gittest.Run: git exits 1 for a key that
	// is not set, which is the answer here and not a failure.
	if got, ok, err := wt.Git.RemotePushURL(ctx, RemoteName); err != nil || ok {
		t.Errorf("pushurl = %q, %v, %v; want it removed", got, ok, err)
	}
	if !SameRepository(wt.PushURL, ours) {
		t.Errorf("PushURL = %q, want the configured %q", wt.PushURL, ours)
	}
	if !strings.Contains(notes.String(), "removing it") {
		t.Errorf("the user was not told: %q", notes.String())
	}
}

// A rewrite rule can come from configuration this CLI has no business editing, so
// the only honest answer is to stop — before any command has done work, and long
// before a commit could land in the wrong repository.
func TestOpenFailsClosedWhenAPushRuleRetargetsTheRepository(t *testing.T) {
	ours := gittest.NewRepo(t, "main")
	theirs := gittest.NewRepo(t, "main")

	opts := openOpts(t, ours)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	// pushInsteadOf rather than a pushurl: this is the half that cannot be
	// removed, so it has to be refused.
	gittest.Run(t, root, "config", "url."+theirs+".pushInsteadOf", ours)

	_, err = Open(ctx, opts)
	if err == nil {
		t.Fatal("expected the redirected push address to be refused")
	}
	for _, want := range []string{ours, theirs, "gitopsRepo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// The gate is on identity, not spelling: a rewrite that names the same repository
// another way is a legitimate configuration and must still work.
func TestOpenAcceptsAPushRuleThatKeepsTheRepository(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	gittest.Run(t, root, "config", "url."+origin+"/.pushInsteadOf", origin)

	wt, err = Open(ctx, opts)
	if err != nil {
		t.Fatalf("a rewrite to the same repository was refused: %v", err)
	}
	defer wt.Close()
	if wt.PushURL != origin+"/" {
		t.Errorf("PushURL = %q, want the rewritten spelling", wt.PushURL)
	}
}

func TestOpenRecoversFromACheckoutWithNoOrigin(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	gittest.Run(t, root, "remote", "remove", RemoteName)

	wt, err = Open(ctx, opts)
	if err != nil {
		t.Fatalf("a checkout with no origin should be re-cloned: %v", err)
	}
	defer wt.Close()
	if _, err := os.Stat(filepath.Join(wt.Root, "README.md")); err != nil {
		t.Errorf("recovery did not produce a usable clone: %v", err)
	}
}

// A differently-spelled URL for the same repository must not throw the cache away
// on every run. It cannot arise from CheckoutDir, which hashes the spelling, but
// it does arise from git rewriting what it records at clone time.
func TestOpenKeepsACacheWhoseOriginIsSpelledDifferently(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	wt, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	root := wt.Root
	gittest.Run(t, root, "remote", "set-url", RemoteName, origin+"/")
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(root, ".git", "cache-marker")
	gittest.WriteFile(t, marker, "same clone\n")

	wt, err = Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()
	if _, err := os.Stat(marker); err != nil {
		t.Error("the cache was re-cloned for a cosmetic URL difference")
	}
}

// Two concurrent deploys sharing one checkout would interleave edits, so the
// second must be told to wait rather than silently queue or proceed.
func TestOpenIsExclusive(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	ctx := context.Background()

	first, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	_, err = Open(ctx, opts)
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
	third, err := Open(ctx, opts)
	if err != nil {
		t.Fatalf("open after Close should succeed: %v", err)
	}
	third.Close()
}

func TestOpenRequiresURL(t *testing.T) {
	if _, err := Open(context.Background(), Options{Runner: command.ExecRunner{}, CacheRoot: t.TempDir()}); err == nil {
		t.Fatal("expected an error for an empty URL")
	}
}

// The same repository is reachable by several URLs, and each must get its own
// checkout rather than fighting over one.
func TestCheckoutDirIsStablePerURL(t *testing.T) {
	root := t.TempDir()
	a := CheckoutDir(root, "git@gitlab.com:acme/gitops.git")
	again := CheckoutDir(root, "git@gitlab.com:acme/gitops.git")
	b := CheckoutDir(root, "https://gitlab.com/acme/gitops.git")

	if a != again {
		t.Errorf("CheckoutDir is not stable: %q vs %q", a, again)
	}
	if a == b {
		t.Error("different URLs should map to different directories")
	}
	// The path must be usable and readable, not just unique.
	if strings.ContainsAny(filepath.Base(a), "@:/") {
		t.Errorf("cache directory name %q should not contain URL punctuation", filepath.Base(a))
	}
}

func TestRepositoryCloseIsIdempotent(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	opts := openOpts(t, origin)
	wt, err := Open(context.Background(), opts)
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
