// Package gitclone holds the primitives for CLI-owned cached git clones: a
// stable cache directory derived from the remote URL, an exclusive lock, and
// verification that a cached checkout still belongs to the URL it was created
// for. Consumers compose the primitives into their own policy — a mutable
// checkout that is refreshed (internal/gitops) or write-once per-tag
// checkouts (internal/template) — because the two share the mechanics but not
// the lifecycle.
package gitclone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/git"
)

// RemoteName is the remote cached checkouts read from.
const RemoteName = "origin"

// CheckoutDir derives a stable cache path from the remote URL. The URL is
// hashed rather than sanitised because the same repository can be named several
// ways (SSH, HTTPS, with or without .git) and because URLs contain characters
// that are awkward in paths.
func CheckoutDir(cacheRoot, url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(cacheRoot, hex.EncodeToString(sum[:])[:16])
}

// CacheRoot returns the default cache root for one consumer, identified by a
// subdirectory name ("gitops", "templates") under the CLI's cache directory.
func CacheRoot(subdir string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	return filepath.Join(base, "intropy", subdir), nil
}

// Lock takes an exclusive, non-blocking lock on the cache entry beside path.
// Non-blocking on purpose: two concurrent runs sharing one checkout would
// interleave work, and telling the user to wait is far better than silently
// queueing behind a run that may itself be stuck on a network operation.
//
// activity names what the user is doing ("deploy", "scaffold") and kind what
// the lock guards ("GitOps checkout", "template cache") — they are all the
// error knows about its caller.
func Lock(path, activity, kind string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another intropy %s is already using the cached %s (%s); wait for it to finish", activity, kind, filepath.Dir(path))
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

// Unlock releases a lock taken by Lock. It is idempotent.
func Unlock(f *os.File) error {
	if f == nil {
		return nil
	}
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// EnsureVerified leaves dir holding a clone of url and nothing else.
//
// The cache directory is named after a hash of the URL, which records what it was
// created for — not what its git config points at now. Nothing stops that config
// from being stale, half-written by an interrupted run, or edited, and every later
// step trusts "origin". So the origin is read back and checked, every time.
//
// A mismatch re-clones rather than failing. This directory is a cache and holds
// nothing worth keeping, so recovering is both safe and the only outcome the
// user wanted.
func EnsureVerified(ctx context.Context, c git.Client, r command.Runner, dir, url string, stderr io.Writer) error {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	switch {
	case err == nil:
		have, ok, err := c.RemoteURL(ctx, RemoteName)
		if err != nil {
			return err
		}
		if ok && SameRepository(have, url) {
			return nil
		}
		if ok {
			fmt.Fprintf(stderr, "the cached checkout in %s has %s as %s, not %s; re-cloning\n", dir, have, RemoteName, url)
		} else {
			fmt.Fprintf(stderr, "the cached checkout in %s has no %s remote; re-cloning\n", dir, RemoteName)
		}
	case errors.Is(err, fs.ErrNotExist):
		// A directory without .git is the debris of an interrupted clone;
		// git clone refuses to write into it, so clear it first.
	default:
		return fmt.Errorf("inspect %s: %w", dir, err)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear %s: %w", dir, err)
	}
	if err := git.CloneManaged(ctx, r, url, dir); err != nil {
		return err
	}

	// Read back what the clone recorded. A clone that did not end up pointing at
	// the requested repository must not be trusted by later steps.
	have, ok, err := c.RemoteURL(ctx, RemoteName)
	if err != nil {
		return err
	}
	if !ok || !SameRepository(have, url) {
		return fmt.Errorf("cloned %s into %s but its %s is %q", url, dir, RemoteName, have)
	}
	return nil
}
