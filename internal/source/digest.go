package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// CommitTagPrefix is prepended to a commit sha to form the image tag CI
// publishes. Kept in one place: if the pipeline's tagging scheme ever differs,
// this is the only line that changes.
const CommitTagPrefix = "sha-"

// DefaultWatchPollInterval is how often the registry is re-asked while
// watching for a commit's images.
const DefaultWatchPollInterval = 2 * time.Second

// watchProgressInterval is how often a "still waiting" line is printed while
// watching, so a long CI build does not look like a hang. One line per poll
// would flood the terminal on a slow build.
const watchProgressInterval = 30 * time.Second

// CommitTag returns the registry tag for a commit.
func CommitTag(commit string) string { return CommitTagPrefix + commit }

// Pin is one image repository resolved to an immutable digest.
type Pin struct {
	// Image is the bare repository, as declared in component.yaml.
	Image string

	// Digest is the manifest digest, in sha256:… form.
	Digest string

	// Tag is the tag the digest was resolved from, kept for messages.
	Tag string
}

// Ref renders the pinned reference in the form kustomize writes.
func (p Pin) Ref() string { return p.Image + "@" + p.Digest }

// Resolver resolves an image reference to a descriptor. It is the seam that
// lets digest resolution be tested against an in-memory registry.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (registry.Descriptor, error)
}

// NewResolver builds the production resolver. Replaced in tests, following the
// newSkillRegistry pattern in cmd/intropy.
var NewResolver = func(userAgent string) (Resolver, error) {
	return registry.NewClient(registry.WithUserAgent(userAgent))
}

// ResolveDigests resolves every image in the component to the digest CI
// published for commit.
//
// The digest comes from whatever the registry actually returns for the tag,
// which is the point of resolving rather than constructing a reference: for a
// multi-architecture build the tag points at an image index, and the index
// digest is what must be pinned. Registries have also been known to convert
// between Docker and OCI manifest types on read, changing the digest, so the
// only safe value is the one this lookup observed.
func ResolveDigests(ctx context.Context, r Resolver, comp *gitops.ComponentConfig, commit string) ([]Pin, error) {
	tag := CommitTag(commit)

	pins := make([]Pin, 0, len(comp.Images))
	for _, img := range comp.Images {
		ref := img.Name + ":" + tag
		desc, err := r.Resolve(ctx, ref)
		if err != nil {
			return nil, resolveError(err, img.Name, tag)
		}
		if desc.Digest == "" {
			return nil, fmt.Errorf("registry returned no digest for %s", ref)
		}
		pins = append(pins, Pin{Image: img.Name, Digest: desc.Digest, Tag: tag})
	}
	return pins, nil
}

// resolveError turns a registry failure into something a caller can act on.
// A missing tag is the single most common case by far — it means the pipeline
// has not finished publishing — and deserves to say so rather than read as a
// generic 404.
func resolveError(err error, image, tag string) error {
	if errors.Is(err, registry.ErrNotFound) {
		// The registry's answer stays wrapped underneath the friendly
		// message: the watch loop recognises the retryable case with
		// errors.Is, without the message growing a "registry: not found:
		// <ref>" tail.
		return &notPublishedError{image: image, tag: tag, cause: err}
	}
	return fmt.Errorf("resolve %s:%s: %w", image, tag, err)
}

// notPublishedError reports the tag CI has not published yet. Its message is
// written for the operator, while Unwrap keeps the registry's answer (and
// through it, registry.ErrNotFound) reachable for errors.Is.
type notPublishedError struct {
	image string
	tag   string
	cause error
}

func (e *notPublishedError) Error() string {
	return fmt.Sprintf("%s has no %s tag yet: the pipeline has not published an image for this commit (or was not triggered for it)", e.image, e.tag)
}

func (e *notPublishedError) Unwrap() error { return e.cause }

// WatchOptions configures WatchResolveDigests.
type WatchOptions struct {
	// PollInterval is how often the registry is re-asked. Zero means
	// DefaultWatchPollInterval.
	PollInterval time.Duration

	// Stderr receives progress lines. Nil discards them.
	Stderr io.Writer

	// progressInterval overrides watchProgressInterval in tests.
	progressInterval time.Duration
}

func (o *WatchOptions) applyDefaults() {
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultWatchPollInterval
	}
	if o.progressInterval <= 0 {
		o.progressInterval = watchProgressInterval
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
}

// WatchResolveDigests is ResolveDigests with a wait: when the tag CI should
// have published is not in the registry yet, it polls until it is rather than
// failing immediately. There is no timeout — like kubectl --watch, the wait
// ends when the image appears or the caller interrupts (Ctrl+C cancels the
// context, which the commands wire to SIGINT).
//
// Only a missing tag is retried. An auth failure, a network error or a
// malformed answer will not improve by waiting, so those fail on the spot.
func WatchResolveDigests(ctx context.Context, r Resolver, comp *gitops.ComponentConfig, commit string, opts WatchOptions) ([]Pin, error) {
	opts.applyDefaults()

	pins, err := ResolveDigests(ctx, r, comp, commit)
	switch {
	case err == nil:
		return pins, nil
	case !errors.Is(err, registry.ErrNotFound):
		return nil, err
	}

	tag := CommitTag(commit)
	fmt.Fprintf(opts.Stderr, "waiting for the %s image(s) to appear in the registry (ctrl+c to stop)\n", tag)

	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()
	started := time.Now()
	lastProgress := started

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		pins, err := ResolveDigests(ctx, r, comp, commit)
		switch {
		case err == nil:
			fmt.Fprintf(opts.Stderr, "%s resolved after %s\n", tag, time.Since(started).Round(time.Second))
			return pins, nil
		case errors.Is(err, registry.ErrNotFound):
			if time.Since(lastProgress) >= opts.progressInterval {
				fmt.Fprintf(opts.Stderr, "  still waiting for %s… (%s elapsed)\n", tag, time.Since(started).Round(time.Second))
				lastProgress = time.Now()
			}
		default:
			return nil, err
		}
	}
}
