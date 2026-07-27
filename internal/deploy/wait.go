package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/argocd"
	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// NewArgoClient builds the production ArgoCD client. Replaced in tests.
var NewArgoClient = func(opts argocd.Options) (ArgoWaiter, error) {
	return argocd.NewClient(opts)
}

// ArgoWaiter is the part of the ArgoCD client this package needs.
type ArgoWaiter interface {
	Wait(ctx context.Context, opts argocd.WaitOptions) (*argocd.Application, error)
}

// WaitOptions configures WaitForSync.
type WaitOptions struct {
	Repository   *gitops.Repository
	DeployCfg    *gitops.DeployConfig
	AppName      string
	Revision     string
	Timeout      time.Duration
	ArgocdServer string
	UserAgent    string
	Stderr       io.Writer
}

// WaitForSync waits for ArgoCD to apply the pushed revision.
//
// The returned bool reports whether ArgoCD was actually observed. False with a
// nil error means it could not be reached — which is not a deployment failure:
// the commit is the deployment, and being unable to watch does not undo it.
func WaitForSync(ctx context.Context, opts WaitOptions) (*argocd.Application, bool, error) {
	creds, err := argocd.LoadCredentials(argocd.ResolveServer(opts.ArgocdServer, opts.DeployCfg.Argocd.Server))
	if err != nil {
		// No credentials is a setup problem, not a failed deployment.
		fmt.Fprintf(opts.Stderr, "warning: not waiting for ArgoCD: %v\n", err)
		return nil, false, nil
	}

	client, err := NewArgoClient(argocd.Options{
		Credentials:  creds,
		AppNamespace: opts.DeployCfg.Argocd.AppNamespace,
		UserAgent:    opts.UserAgent,
	})
	if err != nil {
		fmt.Fprintf(opts.Stderr, "warning: not waiting for ArgoCD: %v\n", err)
		return nil, false, nil
	}

	fmt.Fprintf(opts.Stderr, "waiting for %s on %s\n", opts.AppName, creds.Server)

	app, err := client.Wait(ctx, argocd.WaitOptions{
		App:      opts.AppName,
		Revision: opts.Revision,
		Timeout:  opts.Timeout,
		Contains: revisionContains(opts.Repository),
		Stderr:   opts.Stderr,
	})
	if err != nil {
		// Not being able to reach ArgoCD after a successful push is a warning:
		// the change is committed and ArgoCD will apply it whenever it can.
		if errors.Is(err, argocd.ErrUnreachable) {
			fmt.Fprintf(opts.Stderr, "warning: %v\n", err)
			fmt.Fprintf(opts.Stderr, "the commit is pushed, so the deployment stands; only the wait was skipped\n")
			return nil, false, nil
		}
		return app, true, err
	}
	return app, true, nil
}

// revisionContains answers whether the revision ArgoCD applied includes ours.
//
// Equality is the common case, but not the only success: if another deployment
// lands after ours, ArgoCD syncs a descendant commit and our sha never appears
// on its own. Asking whether ours is an ancestor is what stops the wait hanging
// forever in that case.
//
// The reported revision may not be in the local clone yet — it can be someone
// else's push — so a miss triggers a fetch before concluding anything.
func revisionContains(repo *gitops.Repository) func(context.Context, string, string) (bool, error) {
	return func(ctx context.Context, mine, reported string) (bool, error) {
		if reported == "" {
			return false, nil
		}
		if reported == mine {
			return true, nil
		}

		contains, err := repo.Git.IsAncestor(ctx, mine, reported)
		if err == nil {
			return contains, nil
		}

		// Most likely the reported commit is not local yet.
		if ferr := repo.Git.Fetch(ctx, gitops.RemoteName, repo.Branch); ferr != nil {
			return false, nil
		}
		contains, err = repo.Git.IsAncestor(ctx, mine, reported)
		if err != nil {
			// Still unknown: treat as not-yet rather than failing the wait on a
			// revision we cannot inspect.
			return false, nil
		}
		return contains, nil
	}
}
