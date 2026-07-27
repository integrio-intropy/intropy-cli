package deploy

import (
	"context"
	"fmt"
	"io"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// session is the state every command in this package starts from: a refreshed
// GitOps checkout and the repository-level policy in it.
type session struct {
	repo      *gitops.Repository
	deployCfg *gitops.DeployConfig

	// argocdServer is argocdServer from the user's configuration file, the last
	// resort when neither the flag nor deploy.yaml names one.
	argocdServer string
}

// sessionOptions is the subset of a command's options openSession needs.
type sessionOptions struct {
	GitopsRepo   string
	ArgocdServer string
	CacheRoot    string
	Runner       command.Runner
	Stderr       io.Writer
}

// openSession resolves the GitOps repository, refreshes the cached checkout and
// loads deploy.yaml.
//
// binaries are required up front, before any network or cache work: discovering
// a missing kustomize after cloning wastes the user's time. Which ones is the
// caller's business — sync renders nothing, so demanding kustomize of it would
// be a prerequisite it never uses.
//
// The caller owns the returned repository and must Close it.
func openSession(ctx context.Context, opts sessionOptions, binaries ...string) (*session, error) {
	if err := command.RequireBinaries(binaries...); err != nil {
		return nil, err
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	resolved := cfg.Resolve(config.Flags{GitopsRepo: opts.GitopsRepo, ArgocdServer: opts.ArgocdServer})
	repoURL, err := resolved.RequireGitopsRepo()
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(opts.Stderr, "refreshing %s\n", repoURL)
	repo, err := gitops.Open(ctx, gitops.Options{URL: repoURL, Runner: opts.Runner, CacheRoot: opts.CacheRoot})
	if err != nil {
		return nil, err
	}

	deployCfg, err := gitops.LoadDeployConfig(repo.Root)
	if err != nil {
		repo.Close()
		return nil, err
	}

	return &session{repo: repo, deployCfg: deployCfg, argocdServer: resolved.ArgocdServer}, nil
}

func (s *session) Close() { s.repo.Close() }

// locateComponent finds the component in the GitOps tree and reads its
// component.yaml. domain and system disambiguate a name that occurs more than
// once.
func (s *session) locateComponent(component, domain, system string) (gitops.Coordinate, *gitops.ComponentConfig, error) {
	coord, err := gitops.FindComponent(s.repo.Root, component, domain, system)
	if err != nil {
		return gitops.Coordinate{}, nil, err
	}
	comp, err := gitops.LoadComponentConfig(componentDir(s.repo.Root, coord))
	if err != nil {
		return gitops.Coordinate{}, nil, err
	}
	return coord, comp, nil
}
