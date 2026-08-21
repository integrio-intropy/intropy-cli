package release

import (
	"context"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// target is the component a read command is about, resolved to the registry
// repository holding its releases.
type target struct {
	coord gitops.Coordinate

	// repo is the cached GitOps checkout the coordinate was read from. The
	// caller owns it and must Close it.
	repo *gitops.Repository

	// releasesRepo is where this component's release manifests live.
	releasesRepo string
}

// openTarget resolves a component to its releases repository.
//
// It is the shared prologue of the read commands: which releases exist is a
// registry question, but which registry to ask is recorded in the GitOps
// repository, so the component's declared images have to be read first. The
// checkout is refreshed, which is a cache detail rather than a change to any
// environment.
//
// The caller must Close the returned repository.
func openTarget(ctx context.Context, opts Options) (*target, error) {
	if err := command.RequireBinaries("git"); err != nil {
		return nil, err
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	resolved := cfg.Resolve(config.Flags{GitopsRepo: opts.GitopsRepo})
	repoURL, err := resolved.RequireGitopsRepo()
	if err != nil {
		return nil, err
	}

	repo, err := gitops.Open(ctx, gitops.Options{URL: repoURL, Runner: opts.Runner, CacheRoot: opts.CacheRoot})
	if err != nil {
		return nil, err
	}

	t, err := func() (*target, error) {
		coord, err := gitops.FindComponent(repo.Root, opts.Component, opts.Domain, opts.System)
		if err != nil {
			return nil, err
		}
		comp, err := gitops.LoadComponentConfig(gitops.JoinRel(repo.Root, coord.RelPath()))
		if err != nil {
			return nil, err
		}
		// Releases live beside the images they describe, so a component with no
		// images has nowhere for them to be.
		if len(comp.Images) == 0 {
			return nil, fmt.Errorf("%s declares no images, so it has no releases", coord)
		}
		return &target{coord: coord, repo: repo, releasesRepo: ReleasesRepo(comp.Images[0].Name)}, nil
	}()
	if err != nil {
		repo.Close()
		return nil, err
	}
	return t, nil
}
