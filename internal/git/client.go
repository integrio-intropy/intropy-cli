// Package git wraps the git binary with the operations the deployment
// commands need. It is deliberately narrow: only what is used, and nothing
// that would tempt a caller into arbitrary git.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/command"
)

// Client is a typed wrapper over the git binary rooted at a working directory.
type Client struct {
	Runner command.Runner
	Dir    string
}

// hardening is prepended to every git invocation.
//
// A commit here is built from an exact list of paths, and the callers say so:
// nothing is staged implicitly, and never `add -A`. Ambient configuration can
// defeat that on its own, because a hook is code the repository supplies and git
// runs it while building the commit — a pre-commit hook can stage anything it
// likes, and the result is pushed to the GitOps repository's default branch as if
// this CLI had asked for it. core.hooksPath is the one setting that disables every
// hook at once, including the ones no --no-verify flag covers (post-checkout,
// post-rewrite during a rebase, reference-transaction), and -c beats the
// repository, global and system configuration it could be set in.
//
// Signing is deliberately left alone: an organisation whose remote requires
// signed commits would have every push rejected, and signing was never how a
// commit acquires files it should not contain.
var hardening = []string{
	// A directory that cannot contain a hook, because it is not a directory.
	"-c", "core.hooksPath=" + os.DevNull,

	// A filesystem monitor is a long-lived helper process started on demand; the
	// deployment commands touch a handful of files in a cache and have no use for
	// one.
	"-c", "core.fsmonitor=false",
}

// NonInteractiveEnv is the environment git needs when its output is captured.
//
// Without it git prompts for credentials on /dev/tty, which is still there even
// though this package gives the child no stdin and reads its output into a
// buffer — so a missing credential reads as a hang with no explanation. With it,
// the same situation is an error that says what happened. Credential helpers and
// SSH agents keep working: only the fallback to asking a human is removed.
func NonInteractiveEnv() []string {
	return []string{"GIT_TERMINAL_PROMPT=0"}
}

// DefaultRunner is the runner every git call site should default to.
func DefaultRunner() command.Runner {
	return command.ExecRunner{Env: NonInteractiveEnv()}
}

func (g Client) run(ctx context.Context, args ...string) (string, error) {
	// Concat rather than append: appending to a package-level slice would let two
	// concurrent calls share its backing array.
	stdout, _, err := g.Runner.Run(ctx, g.Dir, "git", slices.Concat(hardening, args)...)
	return strings.TrimSpace(string(stdout)), err
}

// HEAD returns the full commit sha of HEAD. Full, not abbreviated: an
// abbreviated sha is ambiguous as a registry tag and as an ancestry argument.
func (g Client) HEAD(ctx context.Context) (string, error) {
	sha, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return sha, nil
}

// Status returns porcelain status lines limited to paths, or to the whole tree
// when no paths are given.
//
// Untracked files are listed individually rather than collapsed to their
// directory: these lines are shown to the user when a deploy is refused, and
// "component/" is a much less useful answer than the files that are actually
// uncommitted.
func (g Client) Status(ctx context.Context, paths ...string) ([]string, error) {
	args := []string{"status", "--porcelain", "--untracked-files=all"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := g.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("read working tree status: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Fetch updates remote-tracking refs. Callers that are about to reason about
// ancestry must fetch first: a stale origin/<branch> makes a pushed commit look
// unpushed.
func (g Client) Fetch(ctx context.Context, remote string, refspecs ...string) error {
	args := append([]string{"fetch", "--quiet", remote}, refspecs...)
	if _, err := g.run(ctx, args...); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	return nil
}

// DefaultBranch returns the remote's default branch name, preferring the
// symbolic ref that git records at clone time and falling back to querying the
// remote. Hardcoding "main" would be wrong for any repository that never
// renamed from master.
func (g Client) DefaultBranch(ctx context.Context, remote string) (string, error) {
	if out, err := g.run(ctx, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		// refs/remotes/origin/HEAD reads back as "origin/main".
		if _, branch, ok := strings.Cut(out, "/"); ok && branch != "" {
			return branch, nil
		}
	}

	// Freshly cloned mirrors and worktrees created with --single-branch may
	// have no origin/HEAD; ask the remote directly.
	out, err := g.run(ctx, "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", fmt.Errorf("determine default branch of %s: %w", remote, err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		// "ref: refs/heads/main\tHEAD"
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ref: refs/heads/")
		if !ok {
			continue
		}
		if branch, _, _ := strings.Cut(rest, "\t"); branch != "" {
			return branch, nil
		}
	}
	return "", fmt.Errorf("determine default branch of %s: no symref in ls-remote output", remote)
}

// RemoteURL returns the URL configured for remote, and whether it is configured
// at all.
//
// This is the identity of the checkout, and the only way to tell whether a
// cached clone is a clone of the repository the caller asked for: a directory
// name says what it was created for, not what its git config now points at.
func (g Client) RemoteURL(ctx context.Context, remote string) (string, bool, error) {
	url, err := g.run(ctx, "config", "--get", "remote."+remote+".url")
	if err == nil {
		return url, url != "", nil
	}
	// git exits 1 when the key is simply not set, which is an answer rather than
	// a failure to read it.
	if ee, ok := errors.AsType[*command.ExitError](err); ok && ee.Code == 1 {
		return "", false, nil
	}
	return "", false, fmt.Errorf("read the URL of %s: %w", remote, err)
}

// PushURL returns the URL a push to remote would actually reach, with pushurl
// and url.<base>.pushInsteadOf rewrites applied.
//
// It exists to be reported, not compared: what identifies the repository is the
// configured URL, and a caller with a deliberate insteadOf rewrite must not be
// told their own configuration is a mismatch. Showing the resolved URL is what
// makes such a rewrite visible before anything is pushed.
func (g Client) PushURL(ctx context.Context, remote string) (string, error) {
	url, err := g.run(ctx, "remote", "get-url", "--push", remote)
	if err != nil {
		return "", fmt.Errorf("resolve the push URL of %s: %w", remote, err)
	}
	return url, nil
}

// IsAncestor reports whether ancestor is an ancestor of descendant. Equal
// commits count as ancestors, matching git's own semantics.
func (g Client) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, err := g.run(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	// git exits 1 for "not an ancestor" and 128 for a bad revision, so only
	// exit 1 may be read as a clean negative answer.
	if ee, ok := errors.AsType[*command.ExitError](err); ok && ee.Code == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check whether %s is an ancestor of %s: %w", ShortSHA(ancestor), ShortSHA(descendant), err)
}

// Add stages paths.
func (g Client) Add(ctx context.Context, paths ...string) error {
	args := append([]string{"add", "--"}, paths...)
	if _, err := g.run(ctx, args...); err != nil {
		return fmt.Errorf("stage changes: %w", err)
	}
	return nil
}

// StagedPaths lists the paths the index changes relative to HEAD, repository-root
// relative and slash-separated.
//
// This is how a caller checks that staging an exact list of paths produced exactly
// that index, rather than assuming it did.
func (g Client) StagedPaths(ctx context.Context) ([]string, error) {
	out, err := g.run(ctx, "diff", "--cached", "--name-only", "--no-renames", "-z")
	if err != nil {
		return nil, fmt.Errorf("read the staged paths: %w", err)
	}
	return splitNUL(out), nil
}

// CommitPaths lists the paths a commit changed relative to its parent.
//
// StagedPaths answers what was about to be committed; this answers what was, which
// is the last chance to notice a difference before a push makes it everyone's.
func (g Client) CommitPaths(ctx context.Context, rev string) ([]string, error) {
	// --root so a repository's first commit reports its files rather than nothing.
	out, err := g.run(ctx, "diff-tree", "-r", "--root", "--no-commit-id", "--name-only", "--no-renames", "-z", rev)
	if err != nil {
		return nil, fmt.Errorf("read the paths changed by %s: %w", ShortSHA(rev), err)
	}
	return splitNUL(out), nil
}

// splitNUL splits git's -z output. NUL-terminated records are the only form in
// which git never quotes or escapes a path, which matters because these paths are
// compared against ones this CLI produced.
func splitNUL(out string) []string {
	var paths []string
	for p := range strings.SplitSeq(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// CheckoutPaths discards working-tree changes under paths. Always pass an
// explicit path: an unscoped revert would throw away anything else in the
// worktree, including a concurrent run's staged work.
func (g Client) CheckoutPaths(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return errors.New("CheckoutPaths requires at least one path")
	}
	args := append([]string{"checkout", "--"}, paths...)
	if _, err := g.run(ctx, args...); err != nil {
		return fmt.Errorf("discard changes: %w", err)
	}
	return nil
}

// CreateBranch creates name at startPoint and checks it out, resetting it if it
// already exists locally.
//
// The reset is deliberate: the only local branches in a cached checkout are ones
// a previous run of this CLI left behind, and reusing a stale one would build a
// commit on top of whatever that run happened to do.
func (g Client) CreateBranch(ctx context.Context, name, startPoint string) error {
	if _, err := g.run(ctx, "checkout", "-B", name, startPoint); err != nil {
		return fmt.Errorf("create branch %s at %s: %w", name, startPoint, err)
	}
	return nil
}

// Switch checks out an existing ref.
//
// Callers holding a shared cached checkout must switch back to the default
// branch when they are done: the refresh on the next Open resets whatever branch
// is current to the default branch's remote head, which would silently discard a
// feature branch's commits.
func (g Client) Switch(ctx context.Context, ref string) error {
	if _, err := g.run(ctx, "checkout", ref); err != nil {
		return fmt.Errorf("switch to %s: %w", ref, err)
	}
	return nil
}

// ResetHard moves the branch and working tree to ref.
func (g Client) ResetHard(ctx context.Context, ref string) error {
	if _, err := g.run(ctx, "reset", "--hard", ref); err != nil {
		return fmt.Errorf("reset to %s: %w", ref, err)
	}
	return nil
}

// Clean removes untracked files and directories.
func (g Client) Clean(ctx context.Context) error {
	if _, err := g.run(ctx, "clean", "-fdq"); err != nil {
		return fmt.Errorf("clean working tree: %w", err)
	}
	return nil
}

// RevParse resolves any revision expression to a full sha.
func (g Client) RevParse(ctx context.Context, rev string) (string, error) {
	sha, err := g.run(ctx, "rev-parse", rev)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", rev, err)
	}
	return sha, nil
}

// Clone clones url into dir. dir must not already exist.
//
// Hardened like every other invocation: a clone checks out a worktree, which runs
// post-checkout — the first place a repository gets to run code of its own.
func Clone(ctx context.Context, r command.Runner, url, dir string) error {
	args := slices.Concat(hardening, []string{"clone", "--quiet", url, dir})
	if _, _, err := r.Run(ctx, "", "git", args...); err != nil {
		return fmt.Errorf("clone %s: %w", url, err)
	}
	return nil
}

// ShortSHA abbreviates a sha for messages, leaving non-sha revisions alone.
func ShortSHA(rev string) string {
	if len(rev) >= 40 {
		return rev[:7]
	}
	return rev
}

// Commit records the staged changes. Each message becomes a paragraph, which is
// how a subject line and a trailer block are produced without a temporary file:
// git separates -m arguments with a blank line, and trailers only need to be
// the final paragraph to be parseable by `git log --format=%(trailers)`.
//
// Nothing is staged implicitly — there is no -a — so the caller decides exactly
// what the commit contains. --no-verify is belt and braces on top of the
// core.hooksPath in hardening: the two disable hooks by different mechanisms, and
// this is the operation where a hook would do the most damage.
func (g Client) Commit(ctx context.Context, messages ...string) error {
	if len(messages) == 0 {
		return errors.New("Commit requires at least a subject")
	}
	args := []string{"commit", "--quiet", "--no-verify"}
	for _, m := range messages {
		args = append(args, "-m", m)
	}
	if _, err := g.run(ctx, args...); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Tag creates an annotated tag at commit. Annotated rather than lightweight:
// it carries the author, date and message that make the tag worth reading in
// `git log`, which is the only reason the tag exists.
//
// Creating a tag that already exists is an error rather than a silent move —
// a release tag that quietly relocates would misrepresent history.
func (g Client) Tag(ctx context.Context, name, message, commit string) error {
	if _, err := g.run(ctx, "tag", "--annotate", "--message", message, name, commit); err != nil {
		return fmt.Errorf("tag %s: %w", name, err)
	}
	return nil
}

// TagCommit resolves an annotated or lightweight tag to the commit it points
// at. The bool reports whether the tag exists locally at all, so a caller can
// tell "no such tag" from a genuine failure to read it.
func (g Client) TagCommit(ctx context.Context, name string) (string, bool, error) {
	// ^{commit} dereferences an annotated tag to its commit; without it the
	// answer would be the tag object's own sha, which never matches a commit.
	sha, err := g.run(ctx, "rev-parse", "--verify", "--quiet", "refs/tags/"+name+"^{commit}")
	if err == nil {
		return sha, sha != "", nil
	}
	// --quiet makes an unknown ref exit 1 with no output, which is the answer
	// rather than a failure.
	if ee, ok := errors.AsType[*command.ExitError](err); ok && ee.Code == 1 {
		return "", false, nil
	}
	return "", false, fmt.Errorf("resolve tag %s: %w", name, err)
}

// LogCommit is one commit in a range, reduced to what release notes need.
type LogCommit struct {
	SHA     string
	Subject string
}

// LastCommit returns the sha of the most recent commit on rev that changed path,
// and whether there was one.
//
// Merges are not excluded here, unlike Log: this answers "which commit put the
// file in its current state", and a merge that resolved the file really is that
// commit.
func (g Client) LastCommit(ctx context.Context, rev, path string) (string, bool, error) {
	sha, _, found, err := g.LastCommitAt(ctx, rev, path)
	return sha, found, err
}

// LastCommitAt is LastCommit with the commit's timestamp, which is what dates a
// deployment: an overlay changed when the commit that changed it landed.
//
// The committer date rather than the author date. Publish rebases and retries
// when the push races another deployment, which rewrites the committer date and
// leaves the author date at the first attempt; the later of the two is when the
// change actually reached the branch.
func (g Client) LastCommitAt(ctx context.Context, rev, path string) (string, time.Time, bool, error) {
	// NUL-separated for the same reason Log is: the fields are fixed-width here,
	// but one delimiter convention in this file is one fewer thing to get wrong.
	out, err := g.run(ctx, "log", "-1", "--format=%H%x00%cI", rev, "--", path)
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("read last commit for %s: %w", path, err)
	}
	if out == "" {
		return "", time.Time{}, false, nil
	}

	sha, date, ok := strings.Cut(out, "\x00")
	if !ok {
		return "", time.Time{}, false, fmt.Errorf("read last commit for %s: unexpected git log output %q", path, out)
	}
	at, err := time.Parse(time.RFC3339, date)
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("read last commit for %s: parse commit date %q: %w", path, date, err)
	}
	return sha, at, true, nil
}

// Log lists the commits in revRange, most recent first, limited to paths when
// any are given.
//
// The two fields are separated by a NUL and the records by a newline: a commit
// subject may contain anything except a newline, so a printable delimiter
// could appear inside one and split a record in the wrong place.
func (g Client) Log(ctx context.Context, revRange string, paths ...string) ([]LogCommit, error) {
	args := []string{"log", "--format=%H%x00%s", "--no-merges", revRange}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := g.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("read log for %s: %w", revRange, err)
	}
	if out == "" {
		return nil, nil
	}

	var commits []LogCommit
	for line := range strings.SplitSeq(out, "\n") {
		sha, subject, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		commits = append(commits, LogCommit{SHA: sha, Subject: subject})
	}
	return commits, nil
}

// Push pushes refspec to remote.
//
// A rejection is reported as *PushRejectedError so callers can rebase and retry,
// distinguished from an authentication or network failure where retrying is
// pointless.
func (g Client) Push(ctx context.Context, remote, refspec string) error {
	_, err := g.run(ctx, "push", "--quiet", "--no-verify", remote, refspec)
	if err == nil {
		return nil
	}
	if ee, ok := errors.AsType[*command.ExitError](err); ok && isRejection(ee.Stderr) {
		return &PushRejectedError{Remote: remote, Refspec: refspec, Stderr: ee.Stderr}
	}
	return fmt.Errorf("push %s %s: %w", remote, refspec, err)
}

// PushRejectedError reports that the remote refused the push because it has
// commits we do not have.
type PushRejectedError struct {
	Remote  string
	Refspec string
	Stderr  string
}

func (e *PushRejectedError) Error() string {
	return fmt.Sprintf("push to %s was rejected: %s", e.Remote, strings.TrimSpace(e.Stderr))
}

// isRejection recognises a non-fast-forward refusal. git has no distinct exit
// code for it, so the message is the only signal available; the phrases below
// are the ones git itself emits.
func isRejection(stderr string) bool {
	for _, marker := range []string{"[rejected]", "non-fast-forward", "fetch first", "Updates were rejected"} {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
}

// Rebase replays the current branch onto upstream.
//
// A conflict is reported as *RebaseConflictError with the rebase left in
// progress, so the caller can decide — this package must not abort on the
// caller's behalf, because whether a conflict is recoverable is policy.
func (g Client) Rebase(ctx context.Context, upstream string) error {
	_, err := g.run(ctx, "rebase", upstream)
	if err == nil {
		return nil
	}
	if ee, ok := errors.AsType[*command.ExitError](err); ok {
		return &RebaseConflictError{Upstream: upstream, Stderr: ee.Stderr}
	}
	return fmt.Errorf("rebase onto %s: %w", upstream, err)
}

// RebaseConflictError reports a rebase that stopped, leaving the repository
// mid-rebase.
type RebaseConflictError struct {
	Upstream string
	Stderr   string
}

func (e *RebaseConflictError) Error() string {
	return fmt.Sprintf("rebase onto %s stopped: %s", e.Upstream, strings.TrimSpace(e.Stderr))
}

// RebaseAbort restores the state from before a rebase.
func (g Client) RebaseAbort(ctx context.Context) error {
	if _, err := g.run(ctx, "rebase", "--abort"); err != nil {
		return fmt.Errorf("abort rebase: %w", err)
	}
	return nil
}

// Trailers returns the trailer block of a commit message as ordered pairs.
// Duplicate keys are preserved, since a commit may legitimately repeat one.
func (g Client) Trailers(ctx context.Context, rev string) ([]Trailer, error) {
	out, err := g.run(ctx, "log", "-1", "--format=%(trailers:only=true)", rev)
	if err != nil {
		return nil, fmt.Errorf("read trailers of %s: %w", ShortSHA(rev), err)
	}
	var trailers []Trailer
	for line := range strings.SplitSeq(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key == "" {
			continue
		}
		trailers = append(trailers, Trailer{Key: key, Value: strings.TrimSpace(value)})
	}
	return trailers, nil
}

// Trailer is one Key: value line from a commit message's trailer block.
type Trailer struct {
	Key   string
	Value string
}

// String renders the trailer as it appears in a commit message.
func (t Trailer) String() string { return t.Key + ": " + t.Value }
