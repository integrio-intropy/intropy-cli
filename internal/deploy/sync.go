package deploy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// Sync applies an environment's pending GitOps change through ArgoCD.
//
// This is the other half of a manual-sync environment: deploy and promote record
// intent by pushing a commit, and nothing happens until someone with the rights
// to apply it runs this. The authorisation and the audit trail therefore live in
// ArgoCD, evaluated against its own RBAC, rather than in a forge-specific
// approval on a YAML edit.
//
// The revision synced is the commit that last changed the environment's overlay,
// not the branch head. Those are usually the same, but when they are not,
// syncing the branch head would apply commits nobody reviewed.
func Sync(ctx context.Context, opts SyncOptions) error {
	opts.applyDefaults()
	out := opts.output()

	// No kustomize: this renders nothing, and demanding a binary it never runs
	// would be a prerequisite for no reason.
	s, err := openSession(ctx, opts.session(), "git")
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
	if _, err := gitops.ResolveOverlay(repo.Root, coord, comp, opts.Environment); err != nil {
		return err
	}

	overlayRel := coord.OverlayRelPath(opts.Environment)
	revision, found, err := repo.Git.LastCommit(ctx, "HEAD", overlayRel)
	if err != nil {
		return err
	}
	// Defensive: an overlay that resolved must have been committed, so this
	// should not happen. It matters anyway, because ArgoCD reads an empty
	// revision as "whatever the branch holds" — precisely the behaviour this
	// command exists to prevent.
	if !found {
		return fmt.Errorf("no commit in %s has ever changed %s, so there is no reviewed revision to apply.\nDeploy or promote to %s first",
			repo.Branch, overlayRel, opts.Environment)
	}

	// The reviewed-revision guard. If the branch has moved on since the diff was
	// read, the approval was given for something else.
	if opts.Revision != "" && !sameRevision(opts.Revision, revision) {
		return fmt.Errorf("%s's pending change is %s, not %s.\n%s has advanced since you reviewed it — read the new diff before syncing",
			opts.Environment, git.ShortSHA(revision), git.ShortSHA(opts.Revision), overlayRel)
	}

	appName := coord.AppName(opts.Environment)
	client, creds, err := connect(deployCfg, opts.ArgocdServer, s.argocdServer, opts.UserAgent)
	if err != nil {
		// Unlike a deploy, which has already pushed, there is no deployment here
		// without the API call. Nothing has happened, so this is a failure.
		return fmt.Errorf("cannot sync %s: %w", appName, err)
	}

	app, err := client.Get(ctx, appName)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", appName, err)
	}

	// Already applied: syncing again would be a no-op that reads as an action.
	applied, err := revisionContains(repo)(ctx, revision, app.Status.Sync.Revision)
	if err != nil {
		return err
	}
	if applied && app.Synced() {
		return reportSync(out, coord, opts.Environment, env, revision, app, false)
	}

	fmt.Fprintf(out.Stderr, "syncing %s on %s at %s\n", appName, creds.Server, git.ShortSHA(revision))
	if err := client.Sync(ctx, appName, revision); err != nil {
		return err
	}

	if opts.NoWait {
		return reportSync(out, coord, opts.Environment, env, revision, nil, true)
	}

	synced, observed, err := WaitForSync(ctx, WaitOptions{
		Repository:   repo,
		DeployCfg:    deployCfg,
		AppName:      appName,
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
		synced = nil
	}
	return reportSync(out, coord, opts.Environment, env, revision, synced, true)
}

// sameRevision compares a reviewed revision with the pending one, accepting an
// abbreviated sha on either side — people paste what they read in a log.
func sameRevision(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 7 && b[:len(a)] == a
}

// reportSync writes the outcome. requested is false when the application already
// held the revision and no sync was asked for.
//
// EnvironmentConfig does not carry its own name, so the environment is passed
// alongside it rather than derived.
func reportSync(out output, coord gitops.Coordinate, environment string, env gitops.EnvironmentConfig, revision string, app *argocd.Application, requested bool) error {
	appName := coord.AppName(environment)

	if out.Format == OutputJSON {
		res := SyncResult{
			Component:   coord.Component,
			Domain:      coord.Domain,
			System:      coord.System,
			Environment: environment,
			AppName:     appName,
			OverlayPath: coord.OverlayRelPath(environment),
			Revision:    revision,
			Requested:   requested,
			SyncPolicy:  env.Sync,
		}
		if app != nil {
			res.SyncStatus = app.Status.Sync.Status
			res.HealthStatus = app.Status.Health.Status
			res.SyncedRevision = app.Status.Sync.Revision
		}
		enc := json.NewEncoder(out.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return fmt.Errorf("write JSON result: %w", err)
		}
		return nil
	}

	if !requested {
		fmt.Fprintf(out.Stdout, "%s is already synced and healthy at %s; nothing to do\n", appName, git.ShortSHA(revision))
		return nil
	}
	if app != nil {
		fmt.Fprintf(out.Stdout, "synced %s to %s; it is %s and %s\n",
			appName, git.ShortSHA(revision), app.Status.Sync.Status, app.Status.Health.Status)
		return nil
	}
	fmt.Fprintf(out.Stdout, "asked ArgoCD to sync %s to %s\n", appName, git.ShortSHA(revision))
	return nil
}
