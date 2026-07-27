package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
)

// Diff shows the rendered Kubernetes change a sync would apply: the manifests as
// they render at the revision ArgoCD has applied, against the revision
// `deploy sync` would apply next.
//
// This is the review half of a manual-sync environment, and it is not
// `deploy --plan`. A plan diffs a hypothetical uncommitted edit against the
// current worktree, for the person writing the change, and holds everything the
// overlay refers to constant. Here both sides are commits, and everything between
// them counts — a base that moved, several stacked deployments, an environment
// that was never synced at all.
//
// ArgoCD does the rendering rather than a local kustomize build. The Application,
// not the overlay, is the whole input: spec.source.kustomize overrides and the
// installation's kustomize.buildOptions are invisible to a local build, and at an
// approval gate a diff that is not what gets applied is worse than no diff.
func Diff(ctx context.Context, opts DiffOptions) error {
	opts.applyDefaults()
	out := opts.output()

	// No kustomize: nothing is rendered locally, and demanding a binary this
	// command never runs would be a prerequisite for no reason.
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

	rep := diffReport{
		coord:       coord,
		environment: opts.Environment,
		env:         env,
	}
	if rep.pending, err = pendingRevision(ctx, repo, coord, opts.Environment); err != nil {
		return err
	}
	if rep.provenance, err = pendingProvenance(ctx, repo, rep.pending); err != nil {
		return err
	}

	appName := coord.AppName(opts.Environment)
	client, creds, err := connect(deployCfg, opts.ArgocdServer, s.argocdServer, opts.UserAgent)
	if err != nil {
		// ArgoCD's applied revision is an input here, not an observation. A diff
		// against a guessed baseline, read by someone about to approve it, is worse
		// than admitting the baseline is unknown.
		return fmt.Errorf("cannot show what a sync would change: %w", err)
	}
	app, err := client.Get(ctx, appName)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", appName, err)
	}
	rep.app = app
	rep.synced = app.Status.Sync.Revision

	if rep.synced != "" {
		if rep.applied, err = revisionContains(repo)(ctx, rep.pending, rep.synced); err != nil {
			return err
		}
	}

	// Already applied and settled: there is nothing pending to review, and two
	// renders would be spent proving it.
	if rep.applied && app.Synced() {
		return reportDiff(out, rep)
	}

	fmt.Fprintf(out.Stderr, "rendering %s on %s at %s\n", appName, creds.Server, git.ShortSHA(rep.pending))
	after, err := render(ctx, client, appName, rep.pending, "the revision a sync would apply")
	if err != nil {
		// Rendering the pending revision failed, which means a sync of it would
		// fail too. That is the most useful thing this command can report.
		return err
	}

	// Only when there is one. ArgoCD reads an empty revision as "whatever the
	// branch holds", which would render the pending tree as its own baseline and
	// report that nothing changes — immediately before the first ever apply of
	// every resource in the environment.
	var before []byte
	if rep.synced != "" {
		if before, err = render(ctx, client, appName, rep.synced, "the revision ArgoCD has applied"); err != nil {
			return err
		}
	}

	overlay := coord.OverlayRelPath(opts.Environment)
	rep.diff = kustomize.Diff(before, after, rep.fromLabel(overlay), rep.toLabel(overlay), out.palette())
	if rep.removed, err = removedResources(before, after); err != nil {
		return err
	}
	return reportDiff(out, rep)
}

// render asks ArgoCD for one revision's manifests and canonicalises them.
//
// what names which side of the comparison this is, because a render that fails at
// one revision and not the other is common — a baseline often broke, which is why
// it was replaced — and an unlabelled failure leaves nobody able to tell which.
func render(ctx context.Context, client ArgoClient, app, revision, what string) ([]byte, error) {
	res, err := client.Manifests(ctx, app, revision)
	if err != nil {
		return nil, fmt.Errorf("cannot render %s at %s, %s: %w", app, git.ShortSHA(revision), what, err)
	}
	manifests, err := kustomize.NormalizeJSON(res.Manifests)
	if err != nil {
		return nil, fmt.Errorf("read the render of %s at %s: %w", app, git.ShortSHA(revision), err)
	}
	return manifests, nil
}

// removedResources lists what the baseline renders and the pending revision does
// not. A sync from here does not prune, so these are shown as deletions but will
// not leave the cluster — see argocd.Client.Sync.
func removedResources(before, after []byte) ([]string, error) {
	baseline, err := kustomize.Identities(before)
	if err != nil {
		return nil, err
	}
	pending, err := kustomize.Identities(after)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, id := range baseline {
		if !slices.Contains(pending, id) {
			removed = append(removed, id)
		}
	}
	return removed, nil
}

// provenance is what the pending commit says about itself: the trailers an
// approver needs, and nothing more. The digests are already visible in the diff,
// and the coordinate is what was asked for.
type provenance struct {
	Subject      string
	Release      string
	PromotedFrom string
	SourceCommit string
	By           string
}

// Describe renders the one-line account, omitting whatever the commit does not
// carry. Empty when it carries none of it, which is what a hand-edited overlay
// looks like — worth noticing rather than papering over.
func (p provenance) Describe() string {
	var parts []string
	if p.Release != "" {
		parts = append(parts, "release "+p.Release)
	}
	if p.PromotedFrom != "" {
		parts = append(parts, "promoted from "+p.PromotedFrom)
	}
	if p.SourceCommit != "" {
		parts = append(parts, "source commit "+git.ShortSHA(p.SourceCommit))
	}
	if p.By != "" {
		parts = append(parts, "by "+p.By)
	}
	return strings.Join(parts, ", ")
}

// pendingProvenance reads the pending commit's subject and deployment trailers.
//
// This is the read-back the trailer block was written for. An approver's second
// question, after what changes, is who asked for this and where the bits came
// from, and the commit already answers it.
func pendingProvenance(ctx context.Context, repo *gitops.Repository, rev string) (provenance, error) {
	var p provenance

	// rev^! is the commit without its parents, so this is one commit. Log excludes
	// merges, so a merge that changed the overlay yields no subject — the trailers
	// still come through, and a merge commit is not how a deployment is recorded.
	commits, err := repo.Git.Log(ctx, rev+"^!")
	if err != nil {
		return p, err
	}
	if len(commits) > 0 {
		p.Subject = commits[0].Subject
	}

	trailers, err := repo.Git.Trailers(ctx, rev)
	if err != nil {
		return p, err
	}
	for _, t := range trailers {
		switch t.Key {
		case TrailerRelease:
			p.Release = t.Value
		case TrailerPromotedFrom:
			p.PromotedFrom = t.Value
		case TrailerSourceCommit:
			p.SourceCommit = t.Value
		case TrailerBy:
			p.By = t.Value
		}
	}
	return p, nil
}

// diffReport is everything the report needs. A struct rather than a parameter
// list: a review prints the two revisions, what the commit claims, what ArgoCD
// says, and every way the diff might mislead, which is more than reads well as
// arguments.
type diffReport struct {
	coord       gitops.Coordinate
	environment string
	env         gitops.EnvironmentConfig
	provenance  provenance

	// pending is the revision a sync would apply, synced the one ArgoCD reports
	// it has applied. synced is empty when it never has.
	pending string
	synced  string

	// applied reports that ArgoCD holds the pending revision, or a descendant.
	applied bool

	app     *argocd.Application
	diff    string
	removed []string
}

func (r diffReport) changed() bool { return r.diff != "" }

// fromLabel and toLabel head the two sides of the diff. Worded differently from a
// plan's "(current)" and "(planned)" on purpose: those are one edit seen twice,
// these are two revisions of everything the overlay renders.
//
// A baseline with no revision carries no sha, rather than an empty one.
func (r diffReport) fromLabel(overlay string) string {
	if r.synced == "" {
		return overlay + " (never synced)"
	}
	return fmt.Sprintf("%s @ %s (running in %s)", overlay, git.ShortSHA(r.synced), r.environment)
}

func (r diffReport) toLabel(overlay string) string {
	return fmt.Sprintf("%s @ %s (will be applied)", overlay, git.ShortSHA(r.pending))
}

// warnings are the ways this diff does not tell the whole story. Each one exists
// because an approver could otherwise draw a conclusion the diff does not support.
func (r diffReport) warnings() []string {
	var w []string

	// A deletion in the diff is not a deletion in the cluster.
	if n := len(r.removed); n > 0 {
		w = append(w, fmt.Sprintf("sync does not prune: %d resource%s shown as removed will stay in the cluster (%s)",
			n, plural(n), strings.Join(r.removed, ", ")))
	}

	// Both sides come from git, so drift is outside what was compared.
	if r.app != nil && r.synced != "" && r.app.Status.Sync.Status != argocd.SyncSynced {
		w = append(w, fmt.Sprintf("%s is %s at %s: applying will also revert any change made outside git, which this diff does not show",
			r.coord.AppName(r.environment), r.app.Status.Sync.Status, git.ShortSHA(r.synced)))
	}

	// Syncing the pending revision would render the tree as it stood then.
	if r.applied && r.changed() {
		w = append(w, fmt.Sprintf("ArgoCD has applied %s, which is at or beyond the pending commit: syncing %s would render the tree as it stood then, reverting the changes below",
			git.ShortSHA(r.synced), git.ShortSHA(r.pending)))
	}

	// The pending revision is this overlay's history. If ArgoCD renders elsewhere,
	// the diff is still ArgoCD's own — what is doubtful is whether the pending
	// commit has anything to do with it.
	if r.app != nil && r.app.Spec.Source.Path != "" {
		overlay := r.coord.OverlayRelPath(r.environment)
		if path.Clean(r.app.Spec.Source.Path) != overlay {
			w = append(w, fmt.Sprintf("%s renders %s, not %s: the pending commit is that overlay's history and may not affect what ArgoCD reads",
				r.coord.AppName(r.environment), r.app.Spec.Source.Path, overlay))
		}
	}
	return w
}

// reportDiff writes the review. The header and the diff go to stdout so the whole
// thing pipes into a pager or jq; only progress goes to stderr.
//
// A non-empty diff still exits 0. This reports, it does not gate — the gate is
// ArgoCD, which is the point of the step.
func reportDiff(out output, r diffReport) error {
	if out.Format == OutputJSON {
		return writeDiffJSON(out, r)
	}

	fmt.Fprintf(out.Stdout, "%s → %s\n", r.coord, r.environment)
	fmt.Fprintf(out.Stdout, "  pending  %s  %s\n", git.ShortSHA(r.pending), r.provenance.Subject)
	if described := r.provenance.Describe(); described != "" {
		fmt.Fprintf(out.Stdout, "           %s\n", described)
	}
	appName := r.coord.AppName(r.environment)
	if r.synced == "" {
		fmt.Fprintf(out.Stdout, "  synced   —        %s has never been synced\n", appName)
	} else {
		fmt.Fprintf(out.Stdout, "  synced   %s  %s is %s and %s\n",
			git.ShortSHA(r.synced), appName, r.app.Status.Sync.Status, r.app.Status.Health.Status)
	}

	for _, warning := range r.warnings() {
		fmt.Fprintf(out.Stdout, "\nwarning: %s\n", warning)
	}

	switch {
	case r.applied && !r.changed():
		fmt.Fprintf(out.Stdout, "\n%s already holds %s's pending change; nothing to review\n", appName, r.environment)
		return nil
	case !r.changed():
		fmt.Fprintf(out.Stdout, "\nsyncing %s would change nothing: %s renders identically at both revisions\n",
			git.ShortSHA(r.pending), r.environment)
		return nil
	}

	fmt.Fprintf(out.Stdout, "\n%s", r.diff)

	// The full sha, not the abbreviation: sameRevision is a prefix match, and an
	// abbreviation only weakens the guard it is being handed to.
	fmt.Fprintf(out.Stdout, "\napply this with:\n  intropy deploy sync %s --env %s --revision %s\n",
		r.coord.Component, r.environment, r.pending)
	return nil
}

func writeDiffJSON(out output, r diffReport) error {
	res := DiffResult{
		Component:        r.coord.Component,
		Domain:           r.coord.Domain,
		System:           r.coord.System,
		Environment:      r.environment,
		AppName:          r.coord.AppName(r.environment),
		OverlayPath:      r.coord.OverlayRelPath(r.environment),
		Pending:          r.pending,
		Synced:           r.synced,
		Applied:          r.applied,
		Changed:          r.changed(),
		Diff:             r.diff,
		RemovedResources: r.removed,
		Subject:          r.provenance.Subject,
		Release:          r.provenance.Release,
		PromotedFrom:     r.provenance.PromotedFrom,
		SourceCommit:     r.provenance.SourceCommit,
		DeployedBy:       r.provenance.By,
		SyncPolicy:       r.env.Sync,
	}
	if r.app != nil {
		res.SyncStatus = r.app.Status.Sync.Status
		res.HealthStatus = r.app.Status.Health.Status
	}
	enc := json.NewEncoder(out.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return fmt.Errorf("write JSON result: %w", err)
	}
	return nil
}
