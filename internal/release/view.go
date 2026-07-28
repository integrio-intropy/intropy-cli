package release

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// View reads a published release manifest and renders it.
//
// It makes no source-repository, GitOps-remote, or environment change. It does
// refresh the local cached GitOps checkout to locate the component metadata;
// that cache is an implementation detail, not deployment state. View exists so
// generated notes can be sanity-checked before anything is deployed from them.
func View(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	if opts.Version == "" {
		return fmt.Errorf("a version is required")
	}
	if err := command.RequireBinaries("git"); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resolved := cfg.Resolve(config.Flags{GitopsRepo: opts.GitopsRepo})
	repoURL, err := resolved.RequireGitopsRepo()
	if err != nil {
		return err
	}

	repo, err := gitops.Open(ctx, gitops.Options{URL: repoURL, Runner: opts.Runner, CacheRoot: opts.CacheRoot})
	if err != nil {
		return err
	}
	defer repo.Close()

	coord, err := gitops.FindComponent(repo.Root, opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}
	comp, err := gitops.LoadComponentConfig(gitops.JoinRel(repo.Root, coord.RelPath()))
	if err != nil {
		return err
	}
	if len(comp.Images) == 0 {
		return fmt.Errorf("%s declares no images, so it has no releases", coord)
	}

	reg, err := NewRegistry(opts.UserAgent)
	if err != nil {
		return err
	}

	ref := Ref(ReleasesRepo(comp.Images[0].Name), opts.Version)
	m, err := Pull(ctx, reg, ref)
	if err != nil {
		return err
	}
	// The requested OCI tag and the manifest's self-described version must
	// agree. A retagged artifact must not be presented as a different release.
	if m.Version != opts.Version {
		return fmt.Errorf("%s is tagged as release %s, but its manifest declares version %s", ref, opts.Version, m.Version)
	}

	if opts.OutputFormat == OutputJSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	return renderManifest(opts.Stdout, m)
}
