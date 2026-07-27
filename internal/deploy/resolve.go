package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/release"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// origin is where the digests being deployed came from.
type origin struct {
	// State is the source state to record with the deployment. For a release
	// deploy only Commit is populated, read from the manifest: Branch and
	// Dirty describe a working tree, and a release deploy reads none. Nothing
	// downstream needs them — the overlay annotation, the commit subject and
	// the trailers all want the commit alone.
	State source.State

	// Pins is one pin per image declared in component.yaml, in that order.
	Pins []source.Pin

	// Release is the manifest the pins came from, nil when the digests were
	// resolved from the source repository's HEAD.
	Release *release.Manifest
}

// ReleaseVersion is the version the pins came from, empty for a commit deploy.
func (o origin) ReleaseVersion() string {
	if o.Release == nil {
		return ""
	}
	return o.Release.Version
}

// resolveOrigin decides which digests are being deployed.
//
// Without a version this is the current commit: HEAD must be clean and pushed,
// and the digests come from the tags CI published for it. With a version the
// release manifest is authoritative — it already recorded the digests it was
// created by resolving — so no source repository is consulted at all and the
// command works from any directory.
func resolveOrigin(ctx context.Context, opts Options, coord gitops.Coordinate, comp *gitops.ComponentConfig) (origin, error) {
	if opts.Version != "" {
		return resolveFromRelease(ctx, opts, coord, comp)
	}
	return resolveFromHEAD(ctx, opts, comp)
}

// resolveFromHEAD reads the current commit and asks the registry what CI built
// for it.
func resolveFromHEAD(ctx context.Context, opts Options, comp *gitops.ComponentConfig) (origin, error) {
	src, err := source.Inspect(ctx, git.Client{Runner: opts.Runner, Dir: opts.SourceDir}, comp.SourcePaths, opts.AllowDirty)
	if err != nil {
		return origin{}, err
	}
	if len(src.Dirty) > 0 {
		fmt.Fprintf(opts.Stderr, "warning: deploying with %d uncommitted change(s) under the component's source paths\n", len(src.Dirty))
	}

	resolver, err := source.NewResolver(opts.UserAgent)
	if err != nil {
		return origin{}, err
	}
	fmt.Fprintf(opts.Stderr, "resolving %s\n", source.CommitTag(src.ShortCommit()))
	pins, err := source.ResolveDigests(ctx, resolver, comp, src.Commit)
	if err != nil {
		return origin{}, err
	}
	return origin{State: src, Pins: pins}, nil
}

// resolveFromRelease reads the digests a published release recorded.
//
// This is the whole reason a release exists: the manifest is immutable, so the
// digests in it are exactly the bits that were tested, and pinning them needs
// neither the source repository nor a second registry lookup.
func resolveFromRelease(ctx context.Context, opts Options, coord gitops.Coordinate, comp *gitops.ComponentConfig) (origin, error) {
	if len(comp.Images) == 0 {
		return origin{}, fmt.Errorf("%s declares no images, so it has no releases", coord)
	}

	reg, err := release.NewRegistry(opts.UserAgent)
	if err != nil {
		return origin{}, err
	}

	releasesRepo := release.ReleasesRepo(comp.Images[0].Name)
	ref := release.Ref(releasesRepo, opts.Version)
	fmt.Fprintf(opts.Stderr, "reading %s\n", ref)

	m, err := release.Pull(ctx, reg, ref)
	if err != nil {
		if errors.Is(err, release.ErrNotFound) {
			return origin{}, releaseNotFoundError(ctx, reg, err, coord, releasesRepo, opts.Version)
		}
		return origin{}, err
	}
	// The OCI tag is how the operator selected this release, while Version is
	// the manifest's self-description. Both must agree: accepting a manifest
	// copied or retagged under a different version would deploy and record a
	// release other than the one the command line named.
	if m.Version != opts.Version {
		return origin{}, fmt.Errorf("%s is tagged as release %s, but its manifest declares version %s", ref, opts.Version, m.Version)
	}

	pins, extra, err := pinsFromManifest(m, comp, coord, ref)
	if err != nil {
		return origin{}, err
	}
	for _, image := range extra {
		fmt.Fprintf(opts.Stderr, "warning: release %s also records %s, which %s no longer declares in %s; it will not be pinned\n",
			m.Version, image, coord.Component, gitops.ComponentFileName)
	}

	return origin{State: source.State{Commit: m.Source.Commit}, Pins: pins, Release: m}, nil
}

// pinsFromManifest matches the digests a release recorded against the images
// component.yaml declares.
//
// It iterates comp.Images rather than the manifest's, which is what keeps the
// pin order identical to the HEAD path — report and commitSubject index
// Pins[0], and a deploy should pin what the component declares today.
//
// extra names images the release recorded that the component no longer
// declares. That is a warning rather than a failure: dropping an image is a
// normal thing to do, and refusing would make every earlier release
// undeployable.
func pinsFromManifest(m *release.Manifest, comp *gitops.ComponentConfig, coord gitops.Coordinate, ref string) (pins []source.Pin, extra []string, err error) {
	if m.Component != coord.Component {
		return nil, nil, fmt.Errorf("%s describes component %q, not %q.\nThe releases repository is derived from images[0].name in %s, so two components sharing an image repository collide here",
			ref, m.Component, coord.Component, gitops.ComponentFileName)
	}

	digests := make(map[string]string, len(m.Images))
	for _, img := range m.Images {
		digests[img.Name] = img.Digest
	}

	pins = make([]source.Pin, 0, len(comp.Images))
	for _, img := range comp.Images {
		digest, ok := digests[img.Name]
		if !ok {
			return nil, nil, fmt.Errorf("release %s has no digest for %s, which %s declares in %s.\nThe release was cut before that image existed — cut a new one with 'intropy release create %s --version <next>', or deploy the current commit instead",
				m.Version, img.Name, coord.Component, gitops.ComponentFileName, coord.Component)
		}
		// Tag is deliberately empty: it means "the tag this digest was
		// resolved from", and a release deploy resolved nothing from a tag.
		// Filling in sha-<commit> would assert a registry state never observed.
		pins = append(pins, source.Pin{Image: img.Name, Digest: digest})
		delete(digests, img.Name)
	}

	for name := range digests {
		extra = append(extra, name)
	}
	slices.Sort(extra)
	return pins, extra, nil
}

// releaseNotFoundError explains a version that was never published, and lists
// the ones that were.
//
// The listing is decoration on an error path, so its own failure is swallowed:
// being unable to enumerate versions is no reason to replace a clear message
// with an obscure one.
func releaseNotFoundError(ctx context.Context, reg release.Registry, cause error, coord gitops.Coordinate, releasesRepo, version string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "release %s of %s does not exist in %s", version, coord.Component, releasesRepo)
	if versions, err := release.ListVersions(ctx, reg, releasesRepo); err == nil && len(versions) > 0 {
		slices.Sort(versions)
		fmt.Fprintf(&b, "\npublished versions: %s", strings.Join(versions, ", "))
	} else {
		fmt.Fprintf(&b, "\nnothing has been released for %s yet", coord.Component)
	}
	fmt.Fprintf(&b, "\ncreate it from the source repository: intropy release create %s --version %s", coord.Component, version)
	return fmt.Errorf("%w: %s", cause, b.String())
}
