// Package registry provides a generic OCI registry client: authentication
// via the docker credential store, artifact push/pull, tag/digest
// resolution, and image-index handling. It is policy-free — callers decide
// which artifact types and layouts are acceptable.
package registry

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// Client speaks to OCI registries. It carries no opinion about what an
// artifact should look like — that policy lives in the packages that use
// it (internal/release, internal/source, internal/deploy).
type Client struct {
	auth      *auth.Client
	plainHTTP func(registry string) bool
}

type options struct {
	httpClient *http.Client
	userAgent  string
	credential func(ctx context.Context, registry string) (auth.Credential, error)
	plainHTTP  func(registry string) bool
}

// Option customises NewClient.
type Option func(*options)

// WithHTTPClient replaces the default retrying HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithUserAgent sets the User-Agent header on all registry requests.
func WithUserAgent(ua string) Option {
	return func(o *options) { o.userAgent = ua }
}

// WithCredentials replaces the docker credential store as the credential
// source.
func WithCredentials(f func(ctx context.Context, registry string) (auth.Credential, error)) Option {
	return func(o *options) { o.credential = f }
}

// WithPlainHTTP replaces the default policy for which registries are spoken
// to over plain HTTP (local registries only).
func WithPlainHTTP(f func(registry string) bool) Option {
	return func(o *options) { o.plainHTTP = f }
}

// NewClient builds a Client. Without options it authenticates through the
// docker credential store, retries through the oras default HTTP client,
// and speaks plain HTTP only to localhost registries.
func NewClient(opts ...Option) (*Client, error) {
	o := options{plainHTTP: isLocalRegistry}
	for _, opt := range opts {
		opt(&o)
	}

	credential := o.credential
	if credential == nil {
		store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
		if err != nil {
			return nil, fmt.Errorf("load docker credentials: %w", err)
		}
		credential = credentials.Credential(store)
	}

	httpClient := o.httpClient
	if httpClient == nil {
		httpClient = retry.DefaultClient
	}

	authClient := &auth.Client{
		Client:     httpClient,
		Cache:      auth.NewCache(),
		Credential: credential,
	}
	if o.userAgent != "" {
		authClient.SetUserAgent(o.userAgent)
	}

	return &Client{auth: authClient, plainHTTP: o.plainHTTP}, nil
}

func (c *Client) repository(ref Reference) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.Registry + "/" + ref.Repository)
	if err != nil {
		return nil, fmt.Errorf("build repository client: %w", err)
	}

	repo.Client = c.auth
	repo.PlainHTTP = c.plainHTTP(ref.Registry)

	return repo, nil
}

// isLocalRegistry follows the same convention as docker and the oras CLI:
// local registries speak plain HTTP without an explicit opt-in.
func isLocalRegistry(registry string) bool {
	host := registry
	if h, _, err := net.SplitHostPort(registry); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
