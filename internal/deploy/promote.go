package deploy

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
	"github.com/integrio-intropy/intropy-cli/internal/source"
)

// Promote copies the image digests one environment has pinned into another.
//
// It resolves nothing. Not a release version, not a registry tag, not a commit —
// it reads the source overlay and writes those exact digests. That is the whole
// difference between this and Run: a release tag that has been moved, or a
// registry that answers differently than it did an hour ago, cannot change what
// production ends up running.
//
// Two policies from deploy.yaml are enforced here rather than reported, which is
// what makes this the promotion command: the target's promotesFrom must permit
// the edge, and requireSourceHealthy must be satisfied at the snapshot that was
// read. Both refuse before anything is written.
func Promote(ctx context.Context, opts PromoteOptions) error {
	opts.applyDefaults()
	out := opts.output()

	// Promoting an environment into itself would compare an overlay with the edit
	// about to be made to it, and deploy.yaml does not forbid the edge.
	if opts.From == opts.To {
		return fmt.Errorf("--from and --to are both %s; a promotion moves digests between environments", opts.To)
	}

	s, err := openSession(ctx, opts.session(), "git", "kustomize")
	if err != nil {
		return err
	}
	defer s.Close()
	repo, deployCfg := s.repo, s.deployCfg

	// Both environments, so an undefined one is named as such rather than
	// surfacing later as a missing overlay.
	target, err := deployCfg.Environment(opts.To)
	if err != nil {
		return err
	}
	if _, err := deployCfg.Environment(opts.From); err != nil {
		return err
	}
	if err := checkEdge(opts.From, opts.To, target); err != nil {
		return err
	}

	coord, comp, err := s.locateComponent(opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}
	overlayDir, err := gitops.ResolveOverlay(repo.Root, coord, comp, opts.To)
	if err != nil {
		return err
	}

	// Read the source before evaluating policy, so the health check and the write
	// are about the same digests. Reading it afterwards would leave a window in
	// which an auto-syncing source moves on between the two.
	snap, err := readSnapshot(ctx, repo, coord, comp, opts.From)
	if err != nil {
		return err
	}

	sourceHealth := ""
	if target.RequireSourceHealthy {
		app, err := requireSourceHealthy(ctx, repo, s, coord, snap, opts)
		if err != nil {
			return err
		}
		sourceHealth = fmt.Sprintf("%s is %s and %s at %s", coord.AppName(snap.Environment),
			strings.ToLower(app.Status.Sync.Status), strings.ToLower(app.Status.Health.Status), git.ShortSHA(app.Status.Sync.Revision))
	}

	plan, err := BuildPlan(ctx, PlanOptions{
		Repository:     repo,
		Kustomize:      kustomize.Client{Runner: opts.Runner},
		Coordinate:     coord,
		Environment:    opts.To,
		Source:         sourceState(snap),
		ReleaseVersion: snap.Release,
		PromotedFrom:   snap.Environment,
		Pins:           snap.Pins,
		// Not InspectUpstreams: a promotion knows exactly where its digests came
		// from, so comparing the target with its upstreams would only restate the
		// guarantee the command already makes.
		Notes:      promotionNotes(snap, sourceHealth),
		OverlayDir: overlayDir,
		Palette:    out.palette(),
	})
	if err != nil {
		return err
	}

	if plan.Empty() || opts.PlanOnly {
		if opts.PlanOnly && !plan.Empty() {
			defer revertOverlay(ctx, out, plan, repo)
		}
		return report(out, plan, coord, target, "", nil)
	}

	if out.Format == OutputPlain {
		reportPlan(out, plan)
	}

	revision, err := Publish(ctx, PublishOptions{
		Repository: repo,
		Plan:       plan,
		CliVersion: opts.UserAgent,
		Stderr:     out.Stderr,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out.Stderr, "pushed %s to %s\n", git.ShortSHA(revision), repo.Branch)

	// A manual-sync target — which production is — is gated in ArgoCD, so the
	// commit is as far as a promotion can take it. report prints the sync
	// follow-up rather than waiting for a sync that never starts on its own.
	if opts.NoWait || target.Sync == gitops.SyncManual {
		return report(out, plan, coord, target, revision, nil)
	}

	app, observed, err := WaitForSync(ctx, WaitOptions{
		Repository:   repo,
		DeployCfg:    deployCfg,
		AppName:      coord.AppName(opts.To),
		Revision:     revision,
		Timeout:      opts.Timeout,
		ArgocdServer: opts.ArgocdServer,
		ConfigServer: s.argocdServer,
		UserAgent:    opts.UserAgent,
		Stderr:       out.Stderr,
	})
	if err != nil {
		return err
	}
	if !observed {
		app = nil
	}
	return report(out, plan, coord, target, revision, app)
}

// checkEdge refuses a promotion the target does not permit.
//
// deploy.yaml's promotesFrom is a graph, not documentation: dev → prod skips the
// environment that was supposed to prove the bits, and the whole point of
// declaring the edges is that the tool will not let anyone take that shortcut in
// a hurry.
func checkEdge(from, to string, target gitops.EnvironmentConfig) error {
	if slices.Contains(target.PromotesFrom, from) {
		return nil
	}
	if len(target.PromotesFrom) == 0 {
		return fmt.Errorf("%s promotes from nothing: %s declares no promotesFrom for it.\nAdd %s to it, or deploy to %s directly",
			to, gitops.DeployFileName, from, to)
	}
	return fmt.Errorf("%s does not promote from %s.\n%s allows: %s",
		to, from, gitops.DeployFileName, strings.Join(target.PromotesFrom, ", "))
}

// requireSourceHealthy refuses unless ArgoCD reports the source application
// Synced and Healthy at the revision the snapshot was read from.
//
// The revision check is the substance. A source environment that syncs
// automatically can move on between the overlay being read and the health being
// asked about, so a Healthy answer on its own only proves that *something* is
// healthy there — possibly a later deployment of different bits. Requiring
// ArgoCD's applied revision to contain the snapshot's is what makes the answer
// about the digests being copied.
//
// Every failure is fatal, including an unreachable ArgoCD. Deploy treats that as
// a warning because it has already pushed and the commit is the deployment; here
// health is a precondition, and "I could not check" is not "it is fine".
func requireSourceHealthy(ctx context.Context, repo *gitops.Repository, s *session, coord gitops.Coordinate, snap snapshot, opts PromoteOptions) (*argocd.Application, error) {
	appName := coord.AppName(snap.Environment)

	// The same server resolution as the wait: flag, then ARGOCD_SERVER, then
	// deploy.yaml, then the user's configuration. Checking health against one
	// server and waiting against another would be its own kind of wrong answer.
	client, creds, err := connect(s.deployCfg, opts.ArgocdServer, s.argocdServer, opts.UserAgent)
	if err != nil {
		return nil, fmt.Errorf("%s requires %s to be healthy, and ArgoCD could not be consulted: %w.\nNothing was written. requireSourceHealthy is set for %s in %s",
			opts.To, snap.Environment, err, opts.To, gitops.DeployFileName)
	}

	fmt.Fprintf(opts.Stderr, "checking %s on %s\n", appName, creds.Server)
	app, err := client.Get(ctx, appName)
	if err != nil {
		return nil, fmt.Errorf("%s requires %s to be healthy, and its ArgoCD application could not be read: %w.\nNothing was written",
			opts.To, snap.Environment, err)
	}

	if !app.Synced() {
		detail := fmt.Sprintf("sync %s, health %s", app.Status.Sync.Status, app.Status.Health.Status)
		if app.Status.Health.Message != "" {
			detail += fmt.Sprintf(" (%s)", app.Status.Health.Message)
		}
		return nil, fmt.Errorf("%s requires %s to be Synced and Healthy before a promotion, and %s is %s.\nNothing was written. Fix %s first, or clear requireSourceHealthy for %s in %s",
			opts.To, snap.Environment, appName, detail, snap.Environment, opts.To, gitops.DeployFileName)
	}

	// No revision to compare against: the overlay has no history of its own,
	// which cannot happen for an overlay carrying a digest. Refuse rather than
	// silently accept a health answer that proves nothing.
	if snap.Revision == "" {
		return nil, fmt.Errorf("%s requires %s to be healthy at a known revision, and no commit in %s changed %s.\nNothing was written",
			opts.To, snap.Environment, repo.Branch, coord.OverlayRelPath(snap.Environment))
	}

	applied := app.Status.Sync.Revision
	contains, err := revisionContains(repo)(ctx, snap.Revision, applied)
	if err != nil {
		return nil, err
	}
	if !contains {
		return nil, fmt.Errorf("%s is healthy, but at revision %s — not at %s, which is where its current digests were pinned.\nNothing was written: a healthy application at another revision does not show that these bits ran",
			appName, git.ShortSHA(applied), git.ShortSHA(snap.Revision))
	}
	return app, nil
}

// sourceState is the provenance a promotion records: the source environment's
// commit, unchanged.
//
// Copying it rather than recording the promoting machine's HEAD is deliberate.
// The annotation answers "which source commit produced these bits", and that
// answer does not change because the bits moved environments.
func sourceState(snap snapshot) source.State {
	return source.State{Commit: snap.Commit}
}

// promotionNotes are the lines the plan prints under its summary in place of the
// promotesFrom comparison.
func promotionNotes(snap snapshot, sourceHealth string) []string {
	notes := []string{snap.Describe()}
	if sourceHealth != "" {
		notes = append(notes, sourceHealth)
	}
	return notes
}
