// Package deploy orchestrates a deployment: it resolves which image digest a
// commit produced, pins it into one environment's overlay in the GitOps
// repository, and reports what changed.
//
// The mechanics live in narrower packages — command, git, gitops, kustomize,
// registry and source — and this package is the policy that combines them.
package deploy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/integrio-intropy/intropy-cli/internal/config"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// Run resolves the component's digest for the current commit and pins it into
// one environment's overlay.
//
// With PlanOnly set it stops after the diff, having written nothing. Otherwise
// this branch stops there too — committing and pushing arrives with the next
// step — so the overlay edits are always reverted before returning.
func Run(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	// Fail before touching the network or the cache if the tools are absent:
	// discovering a missing kustomize after cloning wastes the user's time.
	if err := command.RequireBinaries("git", "kustomize"); err != nil {
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

	fmt.Fprintf(opts.Stderr, "refreshing %s\n", repoURL)
	repo, err := gitops.Open(ctx, gitops.Options{URL: repoURL, Runner: opts.Runner, CacheRoot: opts.CacheRoot})
	if err != nil {
		return err
	}
	defer repo.Close()

	deployCfg, err := gitops.LoadDeployConfig(repo.Root)
	if err != nil {
		return err
	}
	env, err := deployCfg.Environment(opts.Environment)
	if err != nil {
		return err
	}

	coord, err := gitops.FindComponent(repo.Root, opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}
	compDir := componentDir(repo.Root, coord)
	comp, err := gitops.LoadComponentConfig(compDir)
	if err != nil {
		return err
	}
	overlayDir, err := gitops.ResolveOverlay(repo.Root, coord, comp, opts.Environment)
	if err != nil {
		return err
	}

	src, err := source.Inspect(ctx, git.Client{Runner: opts.Runner, Dir: opts.SourceDir}, comp.SourcePaths, opts.AllowDirty)
	if err != nil {
		return err
	}
	if len(src.Dirty) > 0 {
		fmt.Fprintf(opts.Stderr, "warning: deploying with %d uncommitted change(s) under the component's source paths\n", len(src.Dirty))
	}

	resolver, err := source.NewResolver(opts.UserAgent)
	if err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "resolving %s\n", source.CommitTag(src.ShortCommit()))
	pins, err := source.ResolveDigests(ctx, resolver, comp, src.Commit)
	if err != nil {
		return err
	}

	palette := kustomize.PlainPalette
	if opts.Color {
		palette = kustomize.ColorPalette
	}
	plan, err := BuildPlan(ctx, PlanOptions{
		Repository:  repo,
		Kustomize:   kustomize.Client{Runner: opts.Runner},
		Coordinate:  coord,
		Environment: opts.Environment,
		Source:      src,
		Pins:        pins,
		OverlayDir:  overlayDir,
		Palette:     palette,
	})
	if err != nil {
		return err
	}

	// An empty plan already reverted itself, and --plan must leave no trace, so
	// only the apply path proceeds past here.
	if plan.Empty() || opts.PlanOnly {
		if opts.PlanOnly && !plan.Empty() {
			defer func() {
				if rerr := plan.Revert(ctx, repo); rerr != nil {
					fmt.Fprintf(opts.Stderr, "warning: could not revert the overlay edit: %v\n", rerr)
				}
			}()
		}
		return report(opts, plan, coord, env, "", nil)
	}

	// Print the plan before writing anything, so the diff is on screen even if
	// the push then fails. Not in JSON mode: there the encoded result is the
	// program output, and a diff on the same stream would make it unparseable.
	if opts.OutputFormat == OutputPlain {
		reportPlan(opts, plan)
	}

	revision, err := Publish(ctx, PublishOptions{
		Repository: repo,
		Plan:       plan,
		CliVersion: opts.UserAgent,
		Stderr:     opts.Stderr,
	})
	if err != nil {
		// Publish leaves nothing pushed on failure, but the local commit may
		// exist; the checkout is hard-reset on the next run either way.
		return err
	}
	fmt.Fprintf(opts.Stderr, "pushed %s to %s\n", revision[:7], repo.Branch)

	// A manual-sync environment is gated in ArgoCD, so there is no sync to wait
	// for; --no-wait opts out everywhere else.
	if opts.NoWait || env.Sync == gitops.SyncManual {
		return report(opts, plan, coord, env, revision, nil)
	}

	app, observed, err := WaitForSync(ctx, WaitOptions{
		Repository:   repo,
		DeployCfg:    deployCfg,
		AppName:      coord.AppName(opts.Environment),
		Revision:     revision,
		Timeout:      opts.Timeout,
		ArgocdServer: opts.ArgocdServer,
		UserAgent:    opts.UserAgent,
		Stderr:       opts.Stderr,
	})
	if err != nil {
		return err
	}
	if !observed {
		app = nil
	}
	return report(opts, plan, coord, env, revision, app)
}

func componentDir(root string, c gitops.Coordinate) string {
	return gitops.JoinRel(root, c.RelPath())
}

// reportPlan writes the human summary and diff to stdout.
func reportPlan(opts Options, plan *Plan) {
	fmt.Fprint(opts.Stdout, plan.Summary())
	if plan.ProvenanceOnly() {
		// kustomize propagates commonAnnotations into pod templates, so this
		// still restarts the pods. Say so rather than let it surprise anyone.
		fmt.Fprintf(opts.Stdout, "\nno image digest changed; only the source-commit annotation moves.\nthis still restarts the pods, because kustomize applies commonAnnotations to pod templates.\n")
	}
	fmt.Fprintf(opts.Stdout, "\n%s", plan.Diff)
}

// report writes the final outcome. revision is the pushed sha, empty when
// nothing was pushed.
func report(opts Options, plan *Plan, coord gitops.Coordinate, env gitops.EnvironmentConfig, revision string, app *argocd.Application) error {
	if opts.OutputFormat == OutputJSON {
		return writeJSON(opts, plan, coord, env, revision, app)
	}

	if plan.Empty() {
		fmt.Fprintf(opts.Stdout, "%s is already at %s in %s; nothing to do\n",
			coord, plan.Pins[0].Digest, plan.Environment)
		return nil
	}

	if revision == "" {
		reportPlan(opts, plan)
		fmt.Fprintf(opts.Stderr, "\nplan only: nothing was committed\n")
		return nil
	}

	// A manual-sync environment is gated in ArgoCD, so the commit is as far as
	// a deploy can take it. Waiting would hang on a sync that never starts.
	if env.Sync == gitops.SyncManual {
		fmt.Fprintf(opts.Stdout, "\ncommitted %s to %s. %s syncs manually — run 'intropy deploy sync %s --env %s' to apply it\n",
			revision[:7], plan.Environment, plan.Environment, coord.Component, plan.Environment)
		return nil
	}

	if app != nil {
		fmt.Fprintf(opts.Stdout, "\ncommitted %s; %s is synced and healthy at %s\n",
			revision[:7], coord.AppName(plan.Environment), shortDigest(app.Status.Sync.Revision))
		return nil
	}
	fmt.Fprintf(opts.Stdout, "\ncommitted %s; ArgoCD will sync %s\n", revision[:7], coord.AppName(plan.Environment))
	return nil
}

func writeJSON(opts Options, plan *Plan, coord gitops.Coordinate, env gitops.EnvironmentConfig, revision string, app *argocd.Application) error {
	res := Result{
		Component:    coord.Component,
		Domain:       coord.Domain,
		System:       coord.System,
		Environment:  plan.Environment,
		SourceCommit: plan.Source.Commit,
		AppName:      coord.AppName(plan.Environment),
		OverlayPath:  coord.OverlayRelPath(plan.Environment),
		Changed:      !plan.Empty(),
		Applied:      revision != "",
		Revision:     revision,
		SyncPolicy:   env.Sync,
	}
	if app != nil {
		res.SyncStatus = app.Status.Sync.Status
		res.HealthStatus = app.Status.Health.Status
		res.SyncedRevision = app.Status.Sync.Revision
	}
	for _, pin := range plan.Pins {
		res.Pins = append(res.Pins, ResultPin{
			Image:    pin.Image,
			Previous: plan.Previous[pin.Image],
			Digest:   pin.Digest,
			Tag:      pin.Tag,
		})
	}

	enc := json.NewEncoder(opts.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("write JSON result: %w", err)
	}
	return nil
}
