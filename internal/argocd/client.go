package argocd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Sync and health values this package reasons about. ArgoCD defines more; only
// the terminal ones matter here.
const (
	SyncSynced     = "Synced"
	HealthHealthy  = "Healthy"
	HealthDegraded = "Degraded"
)

// ErrUnreachable reports that the ArgoCD API could not be consulted at all —
// network failure, TLS failure, or a rejected token.
//
// Callers that have already pushed treat this as a warning rather than a
// failure: the commit is the deployment, and not being able to watch it happen
// does not undo it.
var ErrUnreachable = errors.New("argocd unreachable")

// ErrAppNotFound reports a 404 for the named application.
var ErrAppNotFound = errors.New("application not found")

// Client talks to one ArgoCD server.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client

	// appNamespace scopes every request. Not optional in practice: Applications
	// are deployed per customer rather than into the argocd namespace, and
	// omitting it against such an installation returns a 404 that looks exactly
	// like a missing application.
	appNamespace string
}

// Options configures NewClient.
type Options struct {
	Credentials  Credentials
	AppNamespace string

	// HTTP replaces the default client. Tests inject httptest's.
	HTTP *http.Client

	// UserAgent identifies this CLI in ArgoCD's request logs.
	UserAgent string
}

// NewClient builds a client for the given credentials.
func NewClient(opts Options) (*Client, error) {
	if opts.Credentials.Server == "" {
		return nil, errors.New("no ArgoCD server")
	}

	scheme := "https"
	if opts.Credentials.PlainText {
		scheme = "http"
	}
	server := opts.Credentials.Server
	// The argocd CLI stores a host[:port], but tolerate a full URL.
	if strings.Contains(server, "://") {
		parsed, err := url.Parse(server)
		if err != nil {
			return nil, fmt.Errorf("parse ArgoCD server %q: %w", server, err)
		}
		scheme, server = parsed.Scheme, parsed.Host
	}

	httpClient := opts.HTTP
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if opts.Credentials.Insecure {
			// Only when the CLI config says so — the local dev clusters serve
			// self-signed certificates.
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		httpClient = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}

	return &Client{
		base:         &url.URL{Scheme: scheme, Host: server},
		token:        opts.Credentials.Token,
		http:         httpClient,
		appNamespace: opts.AppNamespace,
	}, nil
}

// Application is the subset of an ArgoCD Application this package reads.
type Application struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		// Source.Path is the directory ArgoCD renders. Read only to notice that it
		// is not the overlay whose history a caller is reasoning about; repoURL and
		// targetRevision are deliberately not modelled, because comparing repo URLs
		// means normalising ssh against https against a .git suffix, which
		// false-positives more often than it catches anything.
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status string `json:"status"`
			// Revision is the git sha ArgoCD has actually applied. The whole
			// wait turns on this field.
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"health"`
		OperationState struct {
			Phase   string `json:"phase"`
			Message string `json:"message"`
		} `json:"operationState"`
		Summary struct {
			// Images is what ArgoCD observed running — the live digests, as
			// opposed to what the overlay pins. Ground truth for "what runs".
			Images []string `json:"images"`
		} `json:"summary"`
	} `json:"status"`
}

// Synced reports whether the application is both Synced and Healthy. It is not
// on its own sufficient to conclude a deployment succeeded — see Wait.
func (a *Application) Synced() bool {
	return a.Status.Sync.Status == SyncSynced && a.Status.Health.Status == HealthHealthy
}

// Get reads an application's current state.
func (c *Client) Get(ctx context.Context, app string) (*Application, error) {
	var out Application
	if err := c.do(ctx, http.MethodGet, c.appURL(app, "", nil), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Refresh asks ArgoCD to re-read git now.
//
// Without this the wait would sit through the reconciliation poll interval —
// three minutes by default — before anything could possibly change.
func (c *Client) Refresh(ctx context.Context, app string) error {
	return c.do(ctx, http.MethodGet, c.appURL(app, "", url.Values{"refresh": {"hard"}}), nil, nil)
}

// Sync asks ArgoCD to apply a specific git revision.
//
// The revision is the point of the call. Syncing whatever the branch happens to
// hold would apply commits nobody reviewed, which for a manual-sync production
// application is the one thing the gate exists to prevent.
//
// Prune stays off: a sync triggered from here applies a reviewed image pin, and
// deleting resources that no longer appear in the render is a separate decision
// with a much larger blast radius.
func (c *Client) Sync(ctx context.Context, app, revision string) error {
	body := syncRequest{Name: app, AppNamespace: c.appNamespace, Revision: revision}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode sync request: %w", err)
	}
	return c.do(ctx, http.MethodPost, c.appURL(app, "/sync", nil), bytes.NewReader(encoded), nil)
}

// ManifestResponse is ArgoCD's rendered output for one revision. Each manifest is
// a JSON document, which is the form the repo-server returns them in.
type ManifestResponse struct {
	Manifests []string `json:"manifests"`

	// Revision is the revision ArgoCD resolved and rendered, which is worth
	// reading back: asking for a branch or a tag returns the sha it pointed at.
	Revision string `json:"revision"`
}

// Manifests asks ArgoCD to render the application at a revision.
//
// The render comes from ArgoCD rather than from a local kustomize build because
// the Application, not the overlay, is the whole input: spec.source.kustomize
// overrides and the installation's kustomize.buildOptions are invisible to a
// local build, and a diff that omits them is not the change being approved.
//
// The revision must not be empty. ArgoCD reads that as "whatever the branch
// holds", which for a caller comparing two revisions silently renders the same
// tree twice.
func (c *Client) Manifests(ctx context.Context, app, revision string) (*ManifestResponse, error) {
	if revision == "" {
		return nil, fmt.Errorf("render %s: no revision given", app)
	}
	var out ManifestResponse
	if err := c.do(ctx, http.MethodGet, c.appURL(app, "/manifests", url.Values{"revision": {revision}}), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// syncRequest is ArgoCD's ApplicationSyncRequest, narrowed to the fields this
// CLI sets.
//
// appNamespace is sent in the body as well as the query because that is where
// ArgoCD defines it for this endpoint; omitting it against a per-customer
// installation returns a 404 indistinguishable from a missing application.
type syncRequest struct {
	Name         string `json:"name"`
	AppNamespace string `json:"appNamespace,omitempty"`
	Revision     string `json:"revision"`
	Prune        bool   `json:"prune"`
	DryRun       bool   `json:"dryRun"`
}

// Event is one Kubernetes event about an application.
type Event struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Count   int32  `json:"count"`
}

// Events returns the application's recent events, which is where the reason for
// a stuck sync usually appears.
func (c *Client) Events(ctx context.Context, app string) ([]Event, error) {
	var out struct {
		Items []Event `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, c.appURL(app, "/events", nil), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// appURL builds a request URL. The suffix has to be appended to the path before
// the query is attached — appending it afterwards produces
// "/applications/app?appNamespace=x/events", which is a different endpoint
// entirely and 404s.
func (c *Client) appURL(app, suffix string, extra url.Values) string {
	q := url.Values{}
	for k, vs := range extra {
		q[k] = vs
	}
	if c.appNamespace != "" {
		q.Set("appNamespace", c.appNamespace)
	}

	path := "/api/v1/applications/" + url.PathEscape(app) + suffix
	if len(q) > 0 {
		return path + "?" + q.Encode()
	}
	return path
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Cancellation is the user interrupting, not the server being down.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: %s: %v", ErrUnreachable, c.base.Host, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// Tokens expire, and the raw body is unhelpful. Treated as unreachable
		// so a caller that has already pushed warns rather than fails.
		return fmt.Errorf("%w: %s rejected the token (run 'argocd login %s')", ErrUnreachable, c.base.Host, c.base.Host)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrAppNotFound, describeNotFound(c.appNamespace))
	case resp.StatusCode >= 300:
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, readSnippet(resp.Body))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

// describeNotFound explains the most likely cause. A wrong appNamespace and a
// genuinely absent application return the same 404, and the namespace is the
// far more common mistake, so it is worth naming.
func describeNotFound(appNamespace string) string {
	if appNamespace == "" {
		return "no appNamespace was sent — set argocd.appNamespace in deploy.yaml if Applications live outside the argocd namespace"
	}
	return fmt.Sprintf("not found in namespace %q — check argocd.appNamespace in deploy.yaml", appNamespace)
}

func readSnippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, 2048))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
