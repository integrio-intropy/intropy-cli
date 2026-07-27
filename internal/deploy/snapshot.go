package deploy

import (
	"context"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// snapshot is one environment's overlay as it stands: which digests it pins,
// what it says they came from, and the commit that put it in that state.
//
// A promotion is defined by this value. Everything it writes is copied from
// here, so nothing between reading it and committing can substitute a different
// digest — which is the property that lets someone say production runs the bytes
// staging tested.
type snapshot struct {
	Environment string

	// Pins is one pin per image declared in component.yaml, in that order.
	Pins []source.Pin

	// Commit is the deploy.internal/source-commit annotation, and Release the
	// deploy.internal/release annotation. Release is empty when the environment
	// was deployed from a commit rather than a release.
	Commit  string
	Release string

	// Revision is the GitOps commit that last changed this overlay, empty if the
	// path has no history of its own.
	Revision string
}

// readSnapshot reads what an environment currently has pinned.
//
// Every image the component declares must be pinned to a digest. A tag, or a
// missing entry, is refused rather than skipped: a promotion that copied some
// images and left others alone would produce a target running a mixture no
// environment has ever run.
func readSnapshot(ctx context.Context, repo *gitops.Repository, coord gitops.Coordinate, comp *gitops.ComponentConfig, env string) (snapshot, error) {
	dir, err := gitops.ResolveOverlay(repo.Root, coord, comp, env)
	if err != nil {
		return snapshot{}, err
	}
	k, _, err := kustomize.ReadKustomization(dir)
	if err != nil {
		return snapshot{}, err
	}

	snap := snapshot{
		Environment: env,
		Commit:      k.CommonAnnotations[kustomize.AnnotationSourceCommit],
		Release:     k.CommonAnnotations[kustomize.AnnotationRelease],
	}
	for _, img := range comp.Images {
		entry, found := k.FindImage(img.Name)
		if !found {
			return snapshot{}, fmt.Errorf("%s pins nothing for %s, so there is nothing to promote out of it.\nDeploy to %s first: intropy deploy %s --env %s",
				env, img.Name, env, coord.Component, env)
		}
		if entry.Digest == "" {
			return snapshot{}, fmt.Errorf("%s pins %s at %s rather than a digest, so what it runs is not a fixed set of bits.\nA promotion copies digests — deploy to %s first: intropy deploy %s --env %s",
				env, img.Name, entry.Pinned(), env, coord.Component, env)
		}
		// Tag stays empty on purpose: it means "the tag this digest was resolved
		// from", and a promotion resolved nothing from a tag.
		snap.Pins = append(snap.Pins, source.Pin{Image: img.Name, Digest: entry.Digest})
	}

	revision, _, err := repo.Git.LastCommit(ctx, "HEAD", coord.OverlayRelPath(env))
	if err != nil {
		return snapshot{}, err
	}
	snap.Revision = revision

	return snap, nil
}

// Describe renders the one-line account of where the digests came from, for the
// plan summary.
func (s snapshot) Describe() string {
	if s.Release != "" {
		return fmt.Sprintf("copied from %s, which runs release %s (commit %s)", s.Environment, s.Release, git.ShortSHA(s.Commit))
	}
	if s.Commit != "" {
		return fmt.Sprintf("copied from %s (commit %s)", s.Environment, git.ShortSHA(s.Commit))
	}
	return fmt.Sprintf("copied from %s", s.Environment)
}
