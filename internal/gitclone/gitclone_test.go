//go:build integration

package gitclone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gittest"
)

// ensure runs EnsureVerified against a temporary cache directory. The cache is
// injected rather than redirected through HOME for the same reason the gitops
// suite does it: on macOS a HOME that does not match the passwd entry stalls
// every git invocation.
func ensure(t *testing.T, url string) (context.Context, git.Client, command.Runner, string) {
	t.Helper()
	dir := CheckoutDir(t.TempDir(), url)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	r := command.ExecRunner{}
	return context.Background(), git.NewManagedClient(r, dir), r, dir
}

func TestEnsureVerifiedClonesThenReuses(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	ctx, c, r, dir := ensure(t, origin)
	var notes strings.Builder

	if err := EnsureVerified(ctx, c, r, dir, origin, &notes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("clone is missing README.md: %v", err)
	}

	// A marker inside .git proves the second call kept the same clone rather
	// than paying for another.
	marker := filepath.Join(dir, ".git", "cache-marker")
	gittest.WriteFile(t, marker, "same clone\n")
	if err := EnsureVerified(ctx, c, r, dir, origin, &notes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the cache was re-cloned for an unchanged origin")
	}
}

// An interrupted clone leaves a directory with no .git, which git clone then
// refuses to write into. Without clearing it, the cache would be permanently
// poisoned and every later run would fail.
func TestEnsureVerifiedRecoversFromInterruptedClone(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	ctx, c, r, dir := ensure(t, origin)

	gittest.WriteFile(t, filepath.Join(dir, "partial.txt"), "debris\n")

	if err := EnsureVerified(ctx, c, r, dir, origin, &notesDiscard); err != nil {
		t.Fatalf("should have recovered from a partial clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("recovery did not produce a usable clone: %v", err)
	}
}

// A cached checkout whose origin was pointed at a different repository is not
// the cache this URL paid for, and nothing in it may be trusted.
func TestEnsureVerifiedReclonesWhenOriginMoved(t *testing.T) {
	ours := gittest.NewRepo(t, "main")
	theirs := gittest.NewRepo(t, "main")
	gittest.Commit(t, theirs, "theirs.txt", "someone else's repository\n", "not ours")

	ctx, c, r, dir := ensure(t, ours)
	var notes strings.Builder

	if err := EnsureVerified(ctx, c, r, dir, ours, &notes); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "remote", "set-url", RemoteName, theirs)
	notes.Reset()

	if err := EnsureVerified(ctx, c, r, dir, ours, &notes); err != nil {
		t.Fatal(err)
	}
	have, ok, err := c.RemoteURL(ctx, RemoteName)
	if err != nil || !ok {
		t.Fatalf("RemoteURL = %q, %v, %v", have, ok, err)
	}
	if !SameRepository(have, ours) {
		t.Errorf("origin = %q, want the configured %q", have, ours)
	}
	if _, err := os.Stat(filepath.Join(dir, "theirs.txt")); err == nil {
		t.Error("the checkout still holds the other repository's content")
	}
	if !strings.Contains(notes.String(), "re-cloning") {
		t.Errorf("the user was not told the cache was replaced: %q", notes.String())
	}
}

// A differently-spelled URL for the same repository must not throw the cache
// away. It cannot arise from CheckoutDir, which hashes the spelling, but it
// does arise from git rewriting what it records at clone time.
func TestEnsureVerifiedKeepsAnOriginSpelledDifferently(t *testing.T) {
	origin := gittest.NewRepo(t, "main")
	ctx, c, r, dir := ensure(t, origin)

	if err := EnsureVerified(ctx, c, r, dir, origin, &notesDiscard); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "remote", "set-url", RemoteName, origin+"/")

	marker := filepath.Join(dir, ".git", "cache-marker")
	gittest.WriteFile(t, marker, "same clone\n")
	if err := EnsureVerified(ctx, c, r, dir, origin, &notesDiscard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the cache was re-cloned for a cosmetic URL difference")
	}
}

// Two concurrent runs sharing one checkout would interleave work, so the
// second must be told to wait rather than silently queue or proceed.
func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.lock")

	first, err := Lock(path, "scaffold", "template cache")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Lock(path, "scaffold", "template cache"); err == nil {
		t.Fatal("a second concurrent lock should fail")
	} else {
		for _, want := range []string{"scaffold", "already using"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %q", err, want)
			}
		}
	}

	if err := Unlock(first); err != nil {
		t.Fatal(err)
	}
	third, err := Lock(path, "scaffold", "template cache")
	if err != nil {
		t.Fatalf("lock after Unlock should succeed: %v", err)
	}
	third.Close()
}

func TestUnlockIsIdempotent(t *testing.T) {
	if err := Unlock(nil); err != nil {
		t.Errorf("Unlock(nil) = %v, want nil", err)
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

var notesDiscard strings.Builder
