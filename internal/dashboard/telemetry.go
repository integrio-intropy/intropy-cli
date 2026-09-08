package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Tracing proxy: the flow view starts a system's host (an Aspire AppHost)
// with the default launch profile, which brings up the Aspire dashboard —
// and the dashboard is also the OTLP collector the host's services trace
// into. Dashboards from Aspire 13.2 answer read-only telemetry queries at
// /api/telemetry (resources, traces, traces/{id}) in OTLP JSON, which is
// what the Tracing tab renders instead of sending the user to the Aspire
// UI. Older dashboards are detected by the capability probe below and
// answered with guidance rather than data.
//
// The dashboard is an implementation detail of the host, so the proxy never
// holds a configured address for it: the URL and the browser token are
// scraped from the host's own console output, which `dotnet run` prints on
// startup ("Login to the dashboard at http://localhost:18888/login?t=…"),
// and the token is then exchanged for the telemetry API key the
// /api/telemetry endpoints require (POST /api/telemetry/validateToken).
// Both are rediscovered on every request, so a restart that moves the
// dashboard's port or rotates its token needs no action here.
//
// The proxy's two failure modes are deliberately distinct: a 503 names the
// missing prerequisite (the host is not running, or its dashboard predates
// the telemetry API), while a pass-through of the dashboard's own status
// keeps whatever the dashboard said — only it knows whether a trace exists.

// dashboardLine matches the AppHost's startup banner carrying the dashboard
// URL with its one-time browser token.
var dashboardLine = regexp.MustCompile(`https?://[^\s"]*/login\?t=([A-Za-z0-9_-]+)`)

// telemetryAPIPath is the dashboard URL prefix the exchange, probe, and
// proxy all build on — the one place the prefix lives.
const telemetryAPIPath = "/api/telemetry"

// dashboardExchangeTimeout bounds the token→API-key exchange; proxied
// telemetry requests ride on the request's own context.
const dashboardExchangeTimeout = 10 * time.Second

// telemetryUpstream is the discovered dashboard: its base URL plus the API
// key to send, empty when the dashboard answers unsecured.
type telemetryUpstream struct {
	base   *url.URL
	apiKey string
}

// errTelemetryUnsupported marks a dashboard too old for the telemetry API.
// The proxy serves it as 503 with guidance rather than passing the
// dashboard's login redirect through as if it were data.
var errTelemetryUnsupported = fmt.Errorf("the system's Aspire dashboard predates the telemetry API (Aspire 13.2) — update the system host's Aspire version, or open the Aspire dashboard from the flow view's run logs")

// telemetryMu serializes upstream resolution per system — a burst of Tracing
// tab requests must not run one token exchange each.
var telemetryMu sync.Mutex

// upstreamEntry is the cached resolution for one system: the upstream when
// the dashboard speaks the telemetry API, or the error it answered with
// when it never will (errTelemetryUnsupported).
type upstreamEntry struct {
	up  telemetryUpstream
	err error
}

// upstreamCache holds the last resolved entry per system path, so a run of
// Tracing tab polls (list refresh, then a trace detail) pays the /login?t=
// scrape, token exchange, and capability probe once. Entries are not timed
// out: the dashboard URL is rediscovered from the run logs on every request,
// and a 401 from the dashboard (key rotated by a restart the logs already
// know about) drops the entry for a one-time re-exchange.
var upstreamCache = map[string]upstreamEntry{}

// resolveDashboard finds the dashboard URL and browser token in the run's
// captured logs. The last match wins: a restarted host's newest banner is
// the live dashboard, and stale lines belong to processes already gone.
func resolveDashboard(logs []string) (base *url.URL, token string, ok bool) {
	for i := len(logs) - 1; i >= 0; i-- {
		m := dashboardLine.FindStringSubmatch(logs[i])
		if m == nil {
			continue
		}
		// Trim the /login?t=… suffix for the dashboard's base URL.
		u, err := url.Parse(m[0][:len(m[0])-len("/login?t=")-len(m[1])])
		if err != nil {
			continue
		}
		return u, m[1], true
	}
	return nil, "", false
}

// exchangeToken trades the dashboard's browser token for its telemetry API
// key. An empty key in the answer means the dashboard runs unsecured — the
// API key header is then simply never sent.
//
// The exchange doubles as the capability check: dashboards predating the
// telemetry API have no such endpoint, and answer the POST with a redirect
// to their login page instead of JSON. That redirect is the one signal that
// distinguishes "too old" from "token rejected" (401), so it is mapped to
// errTelemetryUnsupported and everything else surfaces verbatim.
func exchangeToken(ctx context.Context, base *url.URL, token string) (string, error) {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return "", err
	}
	u := base.ResolveReference(&url.URL{Path: telemetryAPIPath + "/validateToken"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := telemetryClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange dashboard token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "", errTelemetryUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange dashboard token: %s", resp.Status)
	}
	var out struct {
		APIKey *string `json:"apiKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("exchange dashboard token: %v", err)
	}
	if out.APIKey == nil {
		return "", nil
	}
	return *out.APIKey, nil
}

// telemetryClient is the HTTP client for every dashboard call the proxy
// makes itself (exchange and probe). Redirects must not be followed: a 3xx
// from /api/telemetry/* is the old-dashboard login page, an answer in
// itself rather than a step toward one.
var telemetryClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// probeTelemetryAPI asks the dashboard for its resource list with the
// exchanged key. The answer classifies the dashboard: 2xx supports the
// telemetry API; a redirect is the pre-13.2 login page; anything else
// (401 on a just-rotated key, 404, a connection refused because the
// dashboard moved) is transient and reported verbatim so the caller's next
// poll retries rather than latching.
func probeTelemetryAPI(ctx context.Context, up telemetryUpstream) error {
	u := up.base.ResolveReference(&url.URL{Path: telemetryAPIPath + "/resources"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if up.apiKey != "" {
		req.Header.Set("X-API-Key", up.apiKey)
	}
	resp, err := telemetryClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe dashboard telemetry API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return errTelemetryUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe dashboard telemetry API: %s", resp.Status)
	}
	return nil
}

// telemetryUpstreamFor resolves the dashboard for one supervised system run:
// scrape the logs, reuse the cached upstream when the URL is unchanged,
// exchange and probe otherwise. A dashboard that fails the capability probe
// is cached as unsupported so subsequent polls answer from cache instead of
// re-probing a dashboard that can never say yes.
func (s *apiServer) telemetryUpstreamFor(ctx context.Context, sys string) (telemetryUpstream, error) {
	s.runMu.Lock()
	run := s.runs[sys]
	var logs []string
	if run != nil && run.handle != nil {
		logs = run.handle.pump.tail()
	}
	s.runMu.Unlock()
	if len(logs) == 0 {
		return telemetryUpstream{}, fmt.Errorf("system is not running: start it from the flow view first")
	}
	base, token, ok := resolveDashboard(logs)
	if !ok {
		return telemetryUpstream{}, fmt.Errorf("no Aspire dashboard in the host's logs — the host predates the dashboard, or has not finished starting")
	}

	telemetryMu.Lock()
	defer telemetryMu.Unlock()
	if entry, ok := upstreamCache[sys]; ok && entry.up.base.String() == base.String() {
		return entry.up, entry.err
	}
	exCtx, cancel := context.WithTimeout(ctx, dashboardExchangeTimeout)
	defer cancel()
	key, err := exchangeToken(exCtx, base, token)
	if err != nil {
		return telemetryUpstream{}, err
	}
	up := telemetryUpstream{base: base, apiKey: key}
	entry := upstreamEntry{up: up}
	if err := probeTelemetryAPI(exCtx, up); err != nil {
		// Only an unsupported dashboard latches; a transient probe failure
		// (connection refused mid-restart, a key the dashboard just rotated)
		// is retried on the next poll, which the uncached exchange already
		// does by not being cached.
		if errors.Is(err, errTelemetryUnsupported) {
			entry.err = err
			upstreamCache[sys] = entry
		}
		return telemetryUpstream{}, err
	}
	upstreamCache[sys] = entry
	return up, nil
}

// telemetryProxy is the reverse proxy shared by the /api/telemetry routes.
// The upstream is resolved per request — the dashboard belongs to a host
// process, not to this server — and the director pins the connection to
// that upstream, rewrites the path into /api/telemetry/…, and attaches the
// API key. A 401 drops the cached key so the next request re-exchanges: a
// restarted dashboard rotates its keys.
var telemetryProxy = &httputil.ReverseProxy{
	Rewrite: func(pr *httputil.ProxyRequest) {
		req := pr.In.Context().Value(telemetryUpstreamKey{}).(telemetryRequest)
		pr.SetURL(req.up.base)
		pr.Out.Host = req.up.base.Host
		pr.Out.URL.Path = telemetryAPIPath + "/" + req.rest
		pr.Out.URL.RawPath = ""
		if req.up.apiKey != "" {
			pr.Out.Header.Set("X-API-Key", req.up.apiKey)
		}
	},
	ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("dashboard: %v", err))
	},
	ModifyResponse: func(resp *http.Response) error {
		if resp.StatusCode == http.StatusUnauthorized {
			telemetryMu.Lock()
			for sys, entry := range upstreamCache {
				if resp.Request != nil && entry.up.base.Host == resp.Request.URL.Host {
					delete(upstreamCache, sys)
					break
				}
			}
			telemetryMu.Unlock()
		}
		return nil
	},
}

// telemetryRequest is what the proxy needs from the incoming request: the
// resolved upstream and the path to ask it for.
type telemetryRequest struct {
	up   telemetryUpstream
	rest string
}

// telemetryUpstreamKey carries the resolved upstream through the proxy's
// request context.
type telemetryUpstreamKey struct{}

// proxyTelemetry serves /api/telemetry/{path...}: the wildcard carries
// <system>/<upstream path>, split on the last slash before the telemetry
// resource segment. A single wildcard is the only shape that works:
// ServeMux segments never match across slashes, so a {system} segment
// cannot hold a root-relative system path (itself slash-separated), and a
// wildcard may only sit at the end of a pattern.
func (s *apiServer) proxyTelemetry(w http.ResponseWriter, r *http.Request) {
	sys, rest := splitTelemetryPath(r.PathValue("path"))
	if rest == "" {
		writeError(w, http.StatusNotFound, "telemetry path must be <system>/<resources|traces|traces/{id}>")
		return
	}
	// The dashboard's API surface is read-only for tracing; a non-GET never
	// has a purpose here.
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "telemetry endpoints are read-only")
		return
	}
	up, err := s.telemetryUpstreamFor(r.Context(), sys)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	r2 := r.WithContext(context.WithValue(r.Context(), telemetryUpstreamKey{}, telemetryRequest{up: up, rest: rest}))
	telemetryProxy.ServeHTTP(w, r2)
}

// splitTelemetryPath divides the {path...} wildcard into the system
// directory and the upstream telemetry path. The upstream path starts at
// the last segment — "resources", "traces", or "logs" — that names a
// telemetry collection, except that a "traces" followed by exactly one
// more segment is a trace detail and owns it. System paths are
// root-relative and slash-separated; "." is the workspace root.
func splitTelemetryPath(p string) (sys, rest string) {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		switch segs[i] {
		case "resources", "logs":
			return joinOrDot(segs[:i]), strings.Join(segs[i:], "/")
		case "traces":
			return joinOrDot(segs[:i]), strings.Join(segs[i:], "/")
		}
	}
	return "", ""
}

func joinOrDot(segs []string) string {
	if len(segs) == 0 {
		return "."
	}
	return strings.Join(segs, "/")
}
