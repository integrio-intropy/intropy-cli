package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/git"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
	"github.com/integrio-intropy/intropy-cli/internal/kustomize"
)

// now is the clock, replaced in tests so a deployment can be dated. Mirrors
// internal/release/create.go.
var now = time.Now

// noValue is what an empty cell renders as, matching the diff report.
const noValue = "—"

// Status reports what every environment runs, side by side.
//
// This is the confirmation at the end of the release process. Promotion copies
// digests rather than rebuilding, so the same digest in every row is what makes
// "production runs the bits staging tested" a fact rather than a hope — and
// until now that was only checkable by opening two kustomization.yaml files and
// asking ArgoCD about each application separately.
//
// It writes nothing: no git, no ArgoCD mutation, and kubectl is never invoked.
func Status(ctx context.Context, opts StatusOptions) error {
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

	coord, comp, err := s.locateComponent(opts.Component, opts.Domain, opts.System)
	if err != nil {
		return err
	}

	environments := statusEnvironments(deployCfg, comp)
	rows := make([]EnvironmentStatus, 0, len(environments))
	for _, env := range environments {
		row := readOverlayState(ctx, repo, coord, comp, env)
		if cfg, err := deployCfg.Environment(env); err == nil {
			row.SyncPolicy = cfg.Sync
		}
		rows = append(rows, row)
	}

	observeArgo(ctx, repo, deployCfg, s, opts, rows)

	return reportStatus(out, coord, comp, rows)
}

// statusEnvironments orders the environments to report.
//
// Promotion order rather than alphabetical: the table's whole claim is about a
// pipeline, and dev, prod, staging tells the wrong story about one. Only the
// environments the component declares are reported — a component need not be
// onboarded everywhere the repository deploys.
//
// An environment the component declares but deploy.yaml does not define is
// appended rather than dropped. It cannot be deployed to, which is exactly the
// sort of thing an operator should see rather than have hidden.
func statusEnvironments(deployCfg *gitops.DeployConfig, comp *gitops.ComponentConfig) []string {
	declared := make(map[string]bool, len(comp.Environments))
	for _, env := range comp.Environments {
		declared[env] = true
	}

	out := make([]string, 0, len(comp.Environments))
	for _, env := range deployCfg.PromotionOrder() {
		if declared[env] {
			out = append(out, env)
			delete(declared, env)
		}
	}
	for _, env := range comp.Environments {
		if declared[env] {
			out = append(out, env)
			delete(declared, env)
		}
	}
	return out
}

// readOverlayState is readSnapshot without the refusals.
//
// readSnapshot rejects an overlay that pins a tag or nothing, because promoting
// out of one would be dishonest about what it copied. Status has the opposite
// duty: an unpinned dev is a fact worth showing next to a correctly pinned prod,
// not a reason to refuse to describe either. Nothing here fails — an
// unreadable environment comes back with Onboarded false and a Reason.
func readOverlayState(ctx context.Context, repo *gitops.Repository, coord gitops.Coordinate, comp *gitops.ComponentConfig, env string) EnvironmentStatus {
	row := EnvironmentStatus{
		Environment: env,
		AppName:     coord.AppName(env),
		OverlayPath: coord.OverlayRelPath(env),
	}

	dir, err := gitops.ResolveOverlay(repo.Root, coord, comp, env)
	if err != nil {
		// The error already explains itself — a missing overlay, or an
		// environment component.yaml does not declare. Its first line is the
		// note; the rest is guidance a table has no room for.
		row.Reason = firstLine(err.Error())
		return row
	}
	k, _, err := kustomize.ReadKustomization(dir)
	if err != nil {
		row.Reason = firstLine(err.Error())
		return row
	}

	row.Onboarded = true
	row.Release = k.CommonAnnotations[kustomize.AnnotationRelease]
	row.SourceCommit = k.CommonAnnotations[kustomize.AnnotationSourceCommit]

	// Every declared image, in declared order, whether or not the overlay pins
	// it: an image missing from the overlay is the most interesting row there
	// is, and omitting it would hide it.
	for _, img := range comp.Images {
		pin := ResultPin{Image: img.Name}
		if entry, found := k.FindImage(img.Name); found {
			pin.Digest, pin.Tag = entry.Digest, entry.NewTag
		}
		row.Pins = append(row.Pins, pin)
	}

	revision, at, found, err := repo.Git.LastCommitAt(ctx, "HEAD", coord.OverlayRelPath(env))
	if err == nil && found {
		row.Revision = revision
		row.DeployedAt = &at
	}
	return row
}

// observeArgo fills in the sync and health of each row, in place.
//
// Unreachable ArgoCD is a warning rather than a failure, unlike sync and diff
// where there is no command at all without the API call. Everything to the left
// of those two columns was read from git and is still true; refusing to print
// the digests because the cluster is unreachable would withhold most of the
// answer over part of it.
func observeArgo(ctx context.Context, repo *gitops.Repository, deployCfg *gitops.DeployConfig, s *session, opts StatusOptions, rows []EnvironmentStatus) {
	client, creds, err := connect(deployCfg, opts.ArgocdServer, s.argocdServer, opts.UserAgent)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warning: not reading sync or health from ArgoCD: %v\n", err)
		return
	}
	fmt.Fprintf(opts.Stderr, "reading %d application(s) from %s\n", len(rows), creds.Server)

	for i := range rows {
		app, err := client.Get(ctx, rows[i].AppName)
		if err != nil {
			if errors.Is(err, argocd.ErrUnreachable) {
				// The server is down or the token is stale. Every remaining
				// request would fail the same way, so stop asking.
				fmt.Fprintf(opts.Stderr, "warning: %v\n", err)
				return
			}
			// A single unknown application: the component is onboarded in git
			// but its Application does not exist, or not under this namespace.
			// The other rows are unaffected.
			fmt.Fprintf(opts.Stderr, "warning: %s: %v\n", rows[i].AppName, err)
			continue
		}

		rows[i].SyncStatus = app.Status.Sync.Status
		rows[i].HealthStatus = app.Status.Health.Status
		rows[i].SyncedRevision = app.Status.Sync.Revision

		// Pending is a git question, not an ArgoCD one: has the commit that
		// last changed this overlay reached the cluster? A descendant counts,
		// which is why this is revisionContains rather than an equality test.
		if rows[i].Revision != "" {
			applied, err := revisionContains(repo)(ctx, rows[i].Revision, app.Status.Sync.Revision)
			if err == nil {
				rows[i].Pending = !applied
			}
		}
	}
}

// pinSignature is an environment's digests, in declared image order.
//
// Empty when the overlay does not pin a digest for every image — there is then
// no set of bits to compare, which is a different thing from disagreeing about
// one.
func pinSignature(row EnvironmentStatus) string {
	if !row.Onboarded || len(row.Pins) == 0 {
		return ""
	}
	digests := make([]string, 0, len(row.Pins))
	for _, pin := range row.Pins {
		if pin.Digest == "" {
			return ""
		}
		digests = append(digests, pin.Digest)
	}
	return strings.Join(digests, " ")
}

// consistent reports whether every onboarded environment pins the identical
// digest for every image.
//
// An environment that pins no digest makes the answer false rather than being
// skipped: the claim is "everything agrees", and an environment nobody can
// compare has not been shown to agree with anything.
func consistent(rows []EnvironmentStatus) bool {
	signature, seen := "", false
	for _, row := range rows {
		if !row.Onboarded {
			continue
		}
		s := pinSignature(row)
		if s == "" {
			return false
		}
		if seen && s != signature {
			return false
		}
		signature, seen = s, true
	}
	return seen
}

// reportStatus writes the table, then the answer to the question that prompted
// it. Exit is always 0: this reports, it does not gate.
func reportStatus(out output, coord gitops.Coordinate, comp *gitops.ComponentConfig, rows []EnvironmentStatus) error {
	if out.Format == OutputJSON {
		res := StatusResult{
			Component:    coord.Component,
			Domain:       coord.Domain,
			System:       coord.System,
			Environments: rows,
			Consistent:   consistent(rows),
		}
		enc := json.NewEncoder(out.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return fmt.Errorf("write JSON result: %w", err)
		}
		return nil
	}

	tw := tabwriter.NewWriter(out.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tENV\tRELEASE\tDIGEST\tAGE\tSYNC\tHEALTH")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			coord.Component, row.Environment, releaseCell(row), digestCell(row),
			ageCell(row), orNone(row.SyncStatus), orNone(row.HealthStatus))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out.Stdout, "\n%s\n", summarise(rows))
	for _, note := range notes(coord, comp, rows) {
		fmt.Fprintf(out.Stdout, "%s\n", note)
	}
	return nil
}

// releaseCell is the version the environment runs.
//
// An environment deployed from a commit has no release annotation but still has
// something true to say, so the source commit stands in. The @ prefix is what
// keeps it from being misread as a version.
func releaseCell(row EnvironmentStatus) string {
	switch {
	case row.Release != "":
		return row.Release
	case row.SourceCommit != "":
		return "@" + git.ShortSHA(row.SourceCommit)
	default:
		return noValue
	}
}

// digestCell is what the overlay pins for the first declared image.
//
// The first only — one row per environment is what makes the table scannable,
// and the multi-image case is covered by a note plus the full list in JSON.
// shortDigest is reused rather than abbreviated further so this is
// character-for-character the digest deploy and promote print.
func digestCell(row EnvironmentStatus) string {
	if !row.Onboarded || len(row.Pins) == 0 {
		return noValue
	}
	switch pin := row.Pins[0]; {
	case pin.Digest != "":
		return shortDigest(pin.Digest)
	case pin.Tag != "":
		return ":" + pin.Tag
	default:
		return noValue
	}
}

func ageCell(row EnvironmentStatus) string {
	if row.DeployedAt == nil {
		return noValue
	}
	return humanAge(now().Sub(*row.DeployedAt))
}

func orNone(s string) string {
	if s == "" {
		return noValue
	}
	return s
}

// humanAge renders how long ago a deployment landed, at one unit.
//
// Truncating rather than rounding: 119 minutes is "1h", never "2h". A
// deployment reading as older than it is would be read as a change someone else
// already reviewed.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

// summarise answers the question the command was asked.
//
// Environments that pin no digest are held apart from those that do rather than
// grouped with each other: "these two run different bits" and "there is nothing
// to compare here" are different findings, and merging them would report a
// disagreement between two environments that were never compared.
func summarise(rows []EnvironmentStatus) string {
	var comparable, unpinned []EnvironmentStatus
	for _, row := range rows {
		switch {
		case !row.Onboarded:
		case pinSignature(row) == "":
			unpinned = append(unpinned, row)
		default:
			comparable = append(comparable, row)
		}
	}

	if len(comparable)+len(unpinned) == 0 {
		return "no environment has a readable overlay, so there is nothing to compare"
	}

	// Group by what each environment runs, keeping first appearance in
	// promotion order so the upstream environment is named first.
	var signatures []string
	byDigest := map[string][]string{}
	for _, row := range comparable {
		s := pinSignature(row)
		if _, seen := byDigest[s]; !seen {
			signatures = append(signatures, s)
		}
		byDigest[s] = append(byDigest[s], row.Environment)
	}

	if len(signatures) == 0 {
		return fmt.Sprintf("%s %s no digest, so there is nothing to compare",
			englishList(environmentNames(unpinned)), conjugate(len(unpinned), "pin"))
	}
	if len(signatures) == 1 && len(unpinned) == 0 {
		if len(comparable) == 1 {
			return fmt.Sprintf("only %s is onboarded, so there is nothing to compare it with", comparable[0].Environment)
		}
		return fmt.Sprintf("all %d environments run %s — these are the same bits, promoted rather than rebuilt",
			len(comparable), describeSignatureString(signatures[0]))
	}

	clauses := make([]string, 0, len(signatures)+1)
	for _, s := range signatures {
		envs := byDigest[s]
		clauses = append(clauses, fmt.Sprintf("%s %s %s", englishList(envs), conjugate(len(envs), "run"), describeSignatureString(s)))
	}
	if len(unpinned) > 0 {
		clauses = append(clauses, fmt.Sprintf("%s %s no digest at all",
			englishList(environmentNames(unpinned)), conjugate(len(unpinned), "pin")))
	}
	return "the environments do not all run the same bits: " + strings.Join(clauses, "; ")
}

func environmentNames(rows []EnvironmentStatus) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Environment)
	}
	return out
}

// conjugate agrees the verb with the number of environments named: one
// environment "runs", several "run".
func conjugate(n int, base string) string {
	if n == 1 {
		return base + "s"
	}
	return base
}

func describeSignatureString(signature string) string {
	digests := strings.Fields(signature)
	if len(digests) == 1 {
		return shortDigest(digests[0])
	}
	short := make([]string, 0, len(digests))
	for _, d := range digests {
		short = append(short, shortDigest(d))
	}
	return strings.Join(short, " + ")
}

// notes are the qualifications under the table: what could not be read, what is
// waiting, and what the table had no room for.
func notes(coord gitops.Coordinate, comp *gitops.ComponentConfig, rows []EnvironmentStatus) []string {
	var out []string
	for _, row := range rows {
		switch {
		case !row.Onboarded:
			out = append(out, fmt.Sprintf("%s: %s", row.Environment, row.Reason))
		default:
			for _, pin := range row.Pins {
				if pin.Digest != "" {
					continue
				}
				if pin.Tag != "" {
					out = append(out, fmt.Sprintf("%s pins %s to the tag %q rather than a digest, so what it runs can change without a deployment",
						row.Environment, pin.Image, pin.Tag))
					continue
				}
				out = append(out, fmt.Sprintf("%s pins nothing for %s, so it has never been deployed there", row.Environment, pin.Image))
			}
		}
	}

	for _, row := range rows {
		if !row.Pending {
			continue
		}
		waiting := "waiting to be applied"
		if row.SyncPolicy == gitops.SyncManual {
			waiting = "waiting on its manual sync gate"
		}
		out = append(out, fmt.Sprintf("%s has a committed change ArgoCD has not applied, %s:\n  intropy deploy diff %s --env %s",
			row.Environment, waiting, coord.Component, row.Environment))
	}

	// The consistency claim above is computed over every image, so it is never
	// narrower than the data behind it — but the table shows one, and a reader
	// should know that before drawing a conclusion from the column.
	if len(comp.Images) > 1 {
		out = append(out, fmt.Sprintf("%s declares %d images; DIGEST shows %s. Use --output json for all of them",
			coord.Component, len(comp.Images), comp.Images[0].Name))
	}
	return out
}

// englishList renders names as "a", "a and b", or "a, b and c".
func englishList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// firstLine trims a multi-line error down to something a note can carry. The
// errors these come from append guidance on later lines, which is useful at a
// prompt and noise under a table row.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
