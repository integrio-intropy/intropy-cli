package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// Trailer keys written into the deployment commit. They are a format, not just
// a message: `deploy history` reads them back, so treat renames as breaking.
const (
	TrailerComponent    = "Deploy-Component"
	TrailerDomain       = "Deploy-Domain"
	TrailerSystem       = "Deploy-System"
	TrailerEnvironment  = "Deploy-Env"
	TrailerImage        = "Deploy-Image"
	TrailerDigest       = "Deploy-Digest"
	TrailerSourceCommit = "Deploy-Source-Commit"
	TrailerBy           = "Deploy-By"
	TrailerCli          = "Deploy-Cli"
)

// maxPushAttempts bounds the rebase-and-retry loop. A one-line change to one
// file replays cleanly, so exhausting this many attempts means the environment
// is being deployed to continuously rather than that the retry is broken.
const maxPushAttempts = 5

// PublishOptions configures Publish.
type PublishOptions struct {
	Repository *gitops.Repository
	Plan       *Plan

	// CliVersion is recorded in a trailer so a bad deploy can be traced to the
	// version that produced it.
	CliVersion string

	// MaxAttempts defaults to maxPushAttempts.
	MaxAttempts int

	// Backoff returns how long to wait before the given attempt (1-based).
	// Defaults to jittered exponential.
	Backoff func(attempt int) time.Duration

	// Sleep defaults to time.Sleep; tests replace it to avoid real delays.
	Sleep func(time.Duration)

	Stderr io.Writer
}

func (o *PublishOptions) applyDefaults() {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = maxPushAttempts
	}
	if o.Backoff == nil {
		o.Backoff = jitteredBackoff
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
}

// Publish commits the overlay change and pushes it, rebasing and retrying if
// someone else pushed first.
//
// It returns the sha that actually landed on the remote. That is not necessarily
// the sha of the first commit: a rebase rewrites it, and every later step — the
// ArgoCD revision gate above all — must reason about the final one.
func Publish(ctx context.Context, opts PublishOptions) (string, error) {
	opts.applyDefaults()

	repo := opts.Repository
	plan := opts.Plan
	rel := plan.RelKustomizationPath(repo.Root)

	// Stage only the file the plan changed. A blanket `git add -A` in a shared
	// checkout would sweep up anything else that happened to be there.
	if err := repo.Git.Add(ctx, rel); err != nil {
		return "", err
	}
	if err := repo.Git.Commit(ctx, commitSubject(plan), commitTrailers(plan, opts.CliVersion)); err != nil {
		return "", err
	}

	refspec := "HEAD:" + repo.Branch
	remoteRef := gitops.RemoteName + "/" + repo.Branch

	for attempt := 1; ; attempt++ {
		err := repo.Git.Push(ctx, gitops.RemoteName, refspec)
		if err == nil {
			return repo.Git.HEAD(ctx)
		}

		var rejected *git.PushRejectedError
		if !errors.As(err, &rejected) {
			// Authentication, network, a missing remote: retrying cannot help.
			return "", err
		}
		if attempt >= opts.MaxAttempts {
			return "", fmt.Errorf("gave up after %d push attempt%s: %s is being deployed to concurrently — try again",
				opts.MaxAttempts, plural(opts.MaxAttempts), repo.Branch)
		}

		fmt.Fprintf(opts.Stderr, "push rejected; rebasing onto %s and retrying (%d/%d)\n", remoteRef, attempt+1, opts.MaxAttempts)
		opts.Sleep(opts.Backoff(attempt))

		if err := repo.Git.Fetch(ctx, gitops.RemoteName, repo.Branch); err != nil {
			return "", err
		}
		if err := repo.Git.Rebase(ctx, remoteRef); err != nil {
			var conflict *git.RebaseConflictError
			if !errors.As(err, &conflict) {
				return "", err
			}
			// A conflict here means someone deployed the *same component to the
			// same environment* in the same moment, so both commits edit the
			// same line. Auto-resolving would silently pick a winner and
			// discard a deployment someone believes succeeded.
			if abortErr := repo.Git.RebaseAbort(ctx); abortErr != nil {
				return "", fmt.Errorf("%w (and the rebase could not be aborted: %v)", err, abortErr)
			}
			return "", fmt.Errorf("another deployment of %s to %s landed at the same moment, and the two changes conflict.\nNothing was pushed. Re-run to deploy on top of theirs",
				plan.Coordinate, plan.Environment)
		}
	}
}

// commitSubject is the one-line summary. The digest is abbreviated: a full one
// makes the subject unreadable in a log, and the trailer carries it in full.
func commitSubject(plan *Plan) string {
	digest := plan.Pins[0].Digest
	if len(plan.Pins) > 1 {
		return fmt.Sprintf("deploy(%s): %s → %d images at %s", plan.Coordinate.Component, plan.Environment, len(plan.Pins), plan.Source.ShortCommit())
	}
	return fmt.Sprintf("deploy(%s): %s → %s (%s)", plan.Coordinate.Component, plan.Environment, shortDigest(digest), plan.Source.ShortCommit())
}

func shortDigest(digest string) string {
	const prefix = "sha256:"
	if hex, ok := strings.CutPrefix(digest, prefix); ok && len(hex) > 12 {
		return prefix + hex[:12]
	}
	return digest
}

// commitTrailers builds the trailer block: one paragraph, one Key: value per
// line, which is what makes it machine-readable by `git log --format=%(trailers)`.
func commitTrailers(plan *Plan, cliVersion string) string {
	trailers := []git.Trailer{
		{Key: TrailerComponent, Value: plan.Coordinate.Component},
		{Key: TrailerDomain, Value: plan.Coordinate.Domain},
		{Key: TrailerSystem, Value: plan.Coordinate.System},
		{Key: TrailerEnvironment, Value: plan.Environment},
	}
	for _, pin := range plan.Pins {
		trailers = append(trailers,
			git.Trailer{Key: TrailerImage, Value: pin.Image},
			git.Trailer{Key: TrailerDigest, Value: pin.Digest},
		)
	}
	trailers = append(trailers, git.Trailer{Key: TrailerSourceCommit, Value: plan.Source.Commit})
	if who := committer(); who != "" {
		trailers = append(trailers, git.Trailer{Key: TrailerBy, Value: who})
	}
	if cliVersion != "" {
		trailers = append(trailers, git.Trailer{Key: TrailerCli, Value: cliVersion})
	}

	var b []byte
	for i, t := range trailers {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, t.String()...)
	}
	return string(b)
}

// committer identifies who ran the deploy, beyond git's own author fields: in
// CI the git identity is often a service account, so the environment is a
// better signal when it is set.
func committer() string {
	for _, key := range []string{"GITLAB_USER_EMAIL", "GITHUB_ACTOR", "USER"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// jitteredBackoff grows the delay and spreads concurrent retries apart, so two
// deploys that collide do not keep colliding in lockstep.
func jitteredBackoff(attempt int) time.Duration {
	base := 100 * time.Millisecond * (1 << min(attempt-1, 4))
	return base + rand.N(base)
}
