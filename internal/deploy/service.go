// Package deploy orchestrates a deployment: it resolves which image digest a
// commit produced, pins it into one environment's overlay in the GitOps
// repository, and reports what changed.
//
// The mechanics live in narrower packages — command, git, gitops, kustomize,
// registry, release and source — and this package is the policy that combines
// them.
package deploy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
)

// Run resolves the digests to deploy — from a release manifest when Version is
// set, otherwise from the current commit — and pins them into one
// environment's overlay.
//
// With PlanOnly set it stops after the diff, having written nothing. Otherwise
// it commits, pushes, and waits for ArgoCD to converge unless the environment
// syncs manually or NoWait is set.
func Run(ctx context.Context, opts Options) error {
	opts.applyDefaults()

	s, err := openSession(ctx, opts.session(), "git", "kustomize")
	if err != nil {
		return err
	}
	defer s.Close()
	repo, deployCfg := s.repo, s.deployCfg

	env, err := deployCfg.Environment(opts.Environment)
	if err != nil {
		return err
	}

	coord, comp, err := s.locateComponent(opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}
	overlayDir, err := gitops.ResolveOverlay(repo.Root, coord, comp, opts.Environment)
	if err != nil {
		return err
	}

	org, err := resolveOrigin(ctx, opts, coord, comp)
	if err != nil {
		return err
	}

	out := opts.output()
	plan, err := BuildPlan(ctx, PlanOptions{
		Repository:     repo,
		Kustomize:      kustomize.Client{Runner: opts.Runner},
		Coordinate:     coord,
		Environment:    opts.Environment,
		Source:         org.State,
		ReleaseVersion: org.ReleaseVersion(),
		Pins:           org.Pins,
		Upstreams:      InspectUpstreams(repo.Root, coord, comp, opts.Environment, env, org.Pins),
		OverlayDir:     overlayDir,
		Palette:        out.palette(),
	})
	if err != nil {
		return err
	}

	// An empty plan already reverted itself, and --plan must leave no trace, so
	// only the apply path proceeds past here.
	if plan.Empty() || opts.PlanOnly {
		if opts.PlanOnly && !plan.Empty() {
			defer revertOverlay(ctx, out, plan, repo)
		}
		return report(out, plan, coord, env, "", nil)
	}

	// Print the plan before writing anything, so the diff is on screen even if
	// the push then fails. Not in JSON mode: there the encoded result is the
	// program output, and a diff on the same stream would make it unparseable.
	if out.Format == OutputPlain {
		reportPlan(out, plan)
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
	fmt.Fprintf(opts.Stderr, "pushed %s to %s\n", git.ShortSHA(revision), repo.Branch)

	// A manual-sync environment is gated in ArgoCD, so there is no sync to wait
	// for; --no-wait opts out everywhere else.
	if opts.NoWait || env.Sync == gitops.SyncManual {
		return report(out, plan, coord, env, revision, nil)
	}

	app, observed, err := WaitForSync(ctx, WaitOptions{
		Repository:   repo,
		DeployCfg:    deployCfg,
		AppName:      coord.AppName(opts.Environment),
		Revision:     revision,
		Timeout:      opts.Timeout,
		ArgocdServer: opts.ArgocdServer,
		ConfigServer: s.argocdServer,
		UserAgent:    opts.UserAgent,
		Stderr:       opts.Stderr,
	})
	if err != nil {
		return err
	}
	if !observed {
		app = nil
	}
	return report(out, plan, coord, env, revision, app)
}

func componentDir(root string, c gitops.Coordinate) string {
	return gitops.JoinRel(root, c.RelPath())
}

// revertOverlay discards the plan's overlay edits, warning if it cannot. The
// cached checkout is shared between runs, so leaving it dirty would poison the
// next one.
func revertOverlay(ctx context.Context, out output, plan *Plan, repo *gitops.Repository) {
	if err := plan.Revert(ctx, repo); err != nil {
		fmt.Fprintf(out.Stderr, "warning: could not revert the overlay edit: %v\n", err)
	}
}

// reportPlan writes the human summary and diff to stdout.
func reportPlan(out output, plan *Plan) {
	fmt.Fprint(out.Stdout, plan.Summary())
	if plan.ProvenanceOnly() {
		// kustomize propagates commonAnnotations into pod templates, so this
		// still restarts the pods. Say so rather than let it surprise anyone.
		fmt.Fprintf(out.Stdout, "\nno image digest changed; only the source-commit annotation moves.\nthis still restarts the pods, because kustomize applies commonAnnotations to pod templates.\n")
	}
	fmt.Fprintf(out.Stdout, "\n%s", plan.Diff)
}

// report writes the final outcome. revision is the pushed sha, empty when
// nothing was pushed.
func report(out output, plan *Plan, coord gitops.Coordinate, env gitops.EnvironmentConfig, revision string, app *argocd.Application) error {
	if out.Format == OutputJSON {
		return writeJSON(out, plan, coord, env, revision, app)
	}

	// Already there. Still print the upstream comparison: "staging is already
	// running the digest dev tested" is the most reassuring form of this
	// message, and suppressing it would withhold the answer to the question
	// that prompted the command.
	if plan.Empty() {
		fmt.Fprintf(out.Stdout, "%s is already at %s in %s; nothing to do\n",
			coord, plan.pinnedAs(), plan.Environment)
		fmt.Fprint(out.Stdout, plan.noteLines())
		return nil
	}

	if revision == "" {
		reportPlan(out, plan)
		fmt.Fprintf(out.Stderr, "\nplan only: nothing was committed\n")
		return nil
	}

	// A manual-sync environment is gated in ArgoCD, so the commit is as far as
	// a deploy can take it. Waiting would hang on a sync that never starts.
	if env.Sync == gitops.SyncManual {
		fmt.Fprintf(out.Stdout, "\ncommitted %s to %s. %s syncs manually — run 'intropy deploy sync %s --env %s' to apply it\n",
			git.ShortSHA(revision), plan.Environment, plan.Environment, coord.Component, plan.Environment)
		return nil
	}

	if app != nil {
		fmt.Fprintf(out.Stdout, "\ncommitted %s; %s is synced and healthy at %s\n",
			git.ShortSHA(revision), coord.AppName(plan.Environment), shortDigest(app.Status.Sync.Revision))
		return nil
	}
	fmt.Fprintf(out.Stdout, "\ncommitted %s; ArgoCD will sync %s\n", git.ShortSHA(revision), coord.AppName(plan.Environment))
	return nil
}

func writeJSON(out output, plan *Plan, coord gitops.Coordinate, env gitops.EnvironmentConfig, revision string, app *argocd.Application) error {
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
		Upstreams:    plan.Upstreams,
		SyncPolicy:   env.Sync,
	}
	res.Release = plan.ReleaseVersion
	res.PromotedFrom = plan.PromotedFrom
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

	enc := json.NewEncoder(out.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("write JSON result: %w", err)
	}
	return nil
}
