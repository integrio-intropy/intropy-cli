// Package gitops models the GitOps repository: a cached, locked checkout of
// it, the directory layout that locates a component, and the two contract
// files (deploy.yaml and component.yaml) that describe what may be deployed
// where.
package gitops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/git"
)

// Repository is a local clone of a GitOps repository, cached between runs and
// held under an exclusive lock for the duration of one.
type Repository struct {
	// Root is the checkout directory.
	Root string

	// URL is the remote this was cloned from.
	URL string

	// Branch is the remote's default branch, the branch deploys land on.
	Branch string

	Git git.Client

	lock *os.File
}

// RemoteName is the remote deploys read from and push to.
const RemoteName = "origin"

// Options configures Open.
type Options struct {
	// URL is the GitOps repository to clone. Required.
	URL string

	// Runner runs git. Defaults to ExecRunner.
	Runner command.Runner

	// CacheRoot overrides where checkouts are cached. Defaults to
	// <user cache dir>/intropy/gitops. Injectable so tests can redirect the
	// cache without reaching for HOME — on macOS os.UserCacheDir derives from
	// it, and overriding HOME makes every git invocation pay a multi-second
	// penalty for a home directory that does not match the passwd entry.
	CacheRoot string
}

// Open returns a clean, up-to-date checkout of the GitOps repository.
//
// The checkout is cached and reused across runs, so a deploy does not pay for a
// full clone every time. Because it is shared, three things are non-negotiable:
// an exclusive lock for the whole run, a hard reset rather than a merge on
// refresh, and removal of untracked files. A previous run killed part-way
// through an edit must not be able to leak that edit into this one's commit.
//
// The caller must Close the result.
func Open(ctx context.Context, opts Options) (*Repository, error) {
	if opts.URL == "" {
		return nil, errors.New("no GitOps repository URL")
	}
	r := opts.Runner
	if r == nil {
		r = command.ExecRunner{}
	}
	url := opts.URL

	cacheRoot := opts.CacheRoot
	if cacheRoot == "" {
		var err error
		if cacheRoot, err = defaultCacheRoot(); err != nil {
			return nil, err
		}
	}
	dir := CheckoutDir(cacheRoot, url)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}

	lock, err := acquireLock(dir + ".lock")
	if err != nil {
		return nil, err
	}

	wt := &Repository{Root: dir, URL: url, lock: lock}
	wt.Git = git.Client{Runner: r, Dir: dir}

	if err := wt.refresh(ctx, r); err != nil {
		wt.Close()
		return nil, err
	}
	return wt, nil
}

func (w *Repository) refresh(ctx context.Context, r command.Runner) error {
	if _, err := os.Stat(filepath.Join(w.Root, ".git")); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect %s: %w", w.Root, err)
		}
		// A directory without .git is the debris of an interrupted clone;
		// git clone refuses to write into it, so clear it first.
		if err := os.RemoveAll(w.Root); err != nil {
			return fmt.Errorf("clear %s: %w", w.Root, err)
		}
		if err := git.Clone(ctx, r, w.URL, w.Root); err != nil {
			return err
		}
	}

	branch, err := w.Git.DefaultBranch(ctx, RemoteName)
	if err != nil {
		return err
	}
	w.Branch = branch

	if err := w.Git.Fetch(ctx, RemoteName, branch); err != nil {
		return err
	}
	// Hard reset, not pull: the cache has no local history worth preserving,
	// and a merge could leave conflict markers in a file we are about to
	// commit.
	if err := w.Git.ResetHard(ctx, RemoteName+"/"+branch); err != nil {
		return err
	}
	return w.Git.Clean(ctx)
}

// Close releases the lock. The checkout itself is deliberately kept.
func (w *Repository) Close() error {
	if w.lock == nil {
		return nil
	}
	err := syscall.Flock(int(w.lock.Fd()), syscall.LOCK_UN)
	if cerr := w.lock.Close(); err == nil {
		err = cerr
	}
	w.lock = nil
	return err
}

func defaultCacheRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	return filepath.Join(base, "intropy", "gitops"), nil
}

// CachedRoot returns the existing cached checkout for the configured GitOps
// repository.
//
// It never clones or fetches. This backs shell completion, which has to be
// instant and must not touch the network — an unconfigured or not-yet-cloned
// repository simply yields no suggestions.
func CachedRoot(gitopsRepoOverride string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	url, err := cfg.Resolve(config.Flags{GitopsRepo: gitopsRepoOverride}).RequireGitopsRepo()
	if err != nil {
		return "", err
	}
	cacheRoot, err := defaultCacheRoot()
	if err != nil {
		return "", err
	}
	dir := CheckoutDir(cacheRoot, url)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("no cached checkout of %s yet", url)
	}
	return dir, nil
}

// CheckoutDir derives a stable cache path from the remote URL. The URL is
// hashed rather than sanitised because the same repository can be named several
// ways (SSH, HTTPS, with or without .git) and because URLs contain characters
// that are awkward in paths.
func CheckoutDir(cacheRoot, url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(cacheRoot, hex.EncodeToString(sum[:])[:16])
}

// acquireLock takes an exclusive, non-blocking lock. Non-blocking on purpose:
// two concurrent deploys sharing one checkout would interleave edits, and
// telling the user to wait is far better than silently queueing behind a run
// that may itself be stuck on a network operation.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another intropy deploy is already using the cached GitOps checkout (%s); wait for it to finish", filepath.Dir(path))
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}
