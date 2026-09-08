// Package xregistry is a read-only client for the Intropy xRegistry
// service: message, endpoint, and schema discovery over GET /export, plus
// in-memory resolution of a message reference into the wiring a component
// needs to subscribe to it.
//
// It shares a name and nothing else with internal/registry, the OCI
// registry client — different protocol, different base URL, different
// failure model. The two packages are unrelated and must not borrow each
// other's types; a rename of one is not a rename of the other.
//
// xRegistry has no message-to-channel field: the association is
// Endpoint.messagegroups (the groups allowed on an endpoint) plus
// Endpoint.channel, and the resolution join in resolve.go walks it. The
// registry declares filtering, inlining, entity-path lookups, and pagination
// out of scope, so the client sends only GET /export — no query params it
// did not see advertised.
package xregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Client speaks to one read-only xRegistry service. Construct with New;
// methods take a context first and never retry internally. The default
// HTTP client has a timeout — tighter or looser policies (and tests)
// pass WithHTTPClient.
type Client struct {
	base       *url.URL
	httpClient *http.Client
	userAgent  string
}

const defaultHTTPTimeout = 15 * time.Second

type options struct {
	httpClient *http.Client
	userAgent  string
}

// Option customises New.
type Option func(*options)

// WithHTTPClient replaces the default client. Tests inject an httptest
// transport here.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithUserAgent sets the User-Agent header on every request.
func WithUserAgent(ua string) Option {
	return func(o *options) { o.userAgent = ua }
}

// New builds a Client for the service at baseURL. The base URL must parse
// and be absolute: a relative base would make every request path a guess.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse xRegistry base URL %q: %w", baseURL, err)
	}
	if !u.IsAbs() {
		return nil, fmt.Errorf("xRegistry base URL %q is not absolute", baseURL)
	}
	o := options{httpClient: &http.Client{Timeout: defaultHTTPTimeout}}
	for _, opt := range opts {
		opt(&o)
	}
	return &Client{base: u, httpClient: o.httpClient, userAgent: o.userAgent}, nil
}

// BaseURL reports the service root the client was built against, without
// a trailing slash.
func (c *Client) BaseURL() string { return c.base.String() }

// get issues one GET and decodes the JSON body into out. Error bodies are
// the xRegistry error document; both the transport error and the body are
// surfaced so the caller can front-load what failed.
func (c *Client) get(ctx context.Context, path string, out any) error {
	u := c.base.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", u.String(), err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: registry returned %s: %s", u.String(), resp.Status, errorBody(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse %s: %w", u.String(), err)
	}
	return nil
}

// maxBodyBytes bounds a response read. The export of a registry that grew
// past this size is itself an operational problem — silently truncating a
// resolution input would be worse than failing loudly.
const maxBodyBytes = 64 << 20

// errorBody condenses an xRegistry error document ({code, detail, status})
// into one line; a body that does not parse is shown truncated as-is.
func errorBody(body []byte) string {
	var doc struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &doc); err == nil && doc.Code != "" {
		if doc.Detail != "" {
			return doc.Code + ": " + doc.Detail
		}
		return doc.Code
	}
	const maxLen = 200
	s := string(body)
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}

// Export fetches the complete registry document. One call, no walk: the
// service guarantees /export contains the message, endpoint, and schema
// views the CLI needs.
func (c *Client) Export(ctx context.Context) (*Export, error) {
	var doc Export
	if err := c.get(ctx, "/export", &doc); err != nil {
		return nil, fmt.Errorf("fetch xRegistry export: %w", err)
	}
	return &doc, nil
}

// DefaultMessage resolves a message's definition attributes from the doc
// view, where they live on the single implicit version.
func (m *Message) DefaultMessage() *MessageVersion {
	if len(m.Versions) == 0 {
		return nil
	}
	// Prefer an explicit default; a document without isdefault markers
	// (none this service emits) falls back to the lowest version id so the
	// result never depends on map iteration order.
	ids := make([]string, 0, len(m.Versions))
	for id := range m.Versions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var fallback *MessageVersion
	for _, id := range ids {
		v := m.Versions[id]
		if v.IsDefault {
			return v
		}
		if fallback == nil {
			fallback = v
		}
	}
	return fallback
}
