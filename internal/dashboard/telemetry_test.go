package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveDashboard(t *testing.T) {
	logs := []string{
		"info: Aspire.Hosting.DistributedApplication[0]",
		`      Login to the dashboard at http://localhost:18888/login?t=abc123token`,
	}
	base, token, ok := resolveDashboard(logs)
	if !ok {
		t.Fatal("no dashboard found in logs")
	}
	if base.String() != "http://localhost:18888" {
		t.Errorf("base = %q, want http://localhost:18888", base)
	}
	if token != "abc123token" {
		t.Errorf("token = %q, want abc123token", token)
	}
}

func TestResolveDashboardPrefersNewestBanner(t *testing.T) {
	logs := []string{
		`Login to the dashboard at http://localhost:18888/login?t=old`,
		`Login to the dashboard at http://localhost:19001/login?t=new`,
	}
	base, token, ok := resolveDashboard(logs)
	if !ok || base.String() != "http://localhost:19001" || token != "new" {
		t.Errorf("base=%v token=%q ok=%v, want the restarted host's banner", base, token, ok)
	}
}

func TestResolveDashboardAbsent(t *testing.T) {
	if _, _, ok := resolveDashboard([]string{"plain dotnet output"}); ok {
		t.Error("found a dashboard in logs without one")
	}
}

// stubDashboard is a fake Aspire dashboard: a token-exchange endpoint and a
// telemetry endpoint, both recording what they received.
type stubDashboard struct {
	srv        *httptest.Server
	gotAPIKey  atomic.Value // string
	tokenCalls atomic.Int32
	traceCalls atomic.Int32
}

func newStubDashboard(t *testing.T) *stubDashboard {
	t.Helper()
	d := &stubDashboard{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/telemetry/validateToken":
			d.tokenCalls.Add(1)
			var body struct {
				Token string `json:"token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token != "the-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"apiKey":"exchanged-key"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/telemetry/resources":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/telemetry/traces"):
			d.traceCalls.Add(1)
			d.gotAPIKey.Store(r.Header.Get("X-API-Key"))
			t.Logf("upstream hit: %s key=%q", r.URL.Path, r.Header.Get("X-API-Key"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"resourceSpans":[]},"totalCount":0,"returnedCount":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func TestProxyTelemetryNotRunning(t *testing.T) {
	root := t.TempDir()
	h := testHandler(t, root)
	rec := get(t, h, "/api/telemetry/sales/ordersync/traces")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not running") {
		t.Errorf("body should name the prerequisite, got %s", rec.Body.String())
	}
}

func TestSplitTelemetryPath(t *testing.T) {
	cases := []struct{ in, sys, rest string }{
		{"sales/ordersync/traces", "sales/ordersync", "traces"},
		{"sales/ordersync/traces/abc123", "sales/ordersync", "traces/abc123"},
		{"sales/ordersync/resources", "sales/ordersync", "resources"},
		{"./traces", ".", "traces"},
		{"traces", ".", "traces"},
		{"sales/ordersync", "", ""},
	}
	for _, c := range cases {
		sys, rest := splitTelemetryPath(c.in)
		if sys != c.sys || rest != c.rest {
			t.Errorf("splitTelemetryPath(%q) = (%q, %q), want (%q, %q)", c.in, sys, rest, c.sys, c.rest)
		}
	}
}

func TestProxyTelemetryEndToEnd(t *testing.T) {
	d := newStubDashboard(t)
	root := t.TempDir()

	h, api, err := newHandler(root, "test", providers{topology: emptyTopo, deploy: emptyDeploy})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	// Supervise a fake host whose logs carry the dashboard banner — the same
	// seam run_test.go uses, with the pump pre-filled.
	run := &systemRun{handle: &runHandle{pump: &logPump{}}}
	run.handle.pump.add("info: Aspire.Hosting.DistributedApplication[0]")
	run.handle.pump.add("      Login to the dashboard at " + d.srv.URL + "/login?t=the-token")
	t.Logf("banner line: Login to the dashboard at %s/login?t=the-token", d.srv.URL)
	api.runMu.Lock()
	api.runs = map[string]*systemRun{"sales/ordersync": run}
	api.runMu.Unlock()

	// Upstream cache is package state: isolate the test from earlier runs.
	telemetryMu.Lock()
	delete(upstreamCache, "sales/ordersync")
	telemetryMu.Unlock()
	t.Cleanup(func() {
		telemetryMu.Lock()
		delete(upstreamCache, "sales/ordersync")
		telemetryMu.Unlock()
	})

	rec := get(t, h, "/api/telemetry/sales/ordersync/traces?limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "resourceSpans") {
		t.Errorf("body should be the dashboard's payload, got %s", rec.Body.String())
	}
	if got, _ := d.gotAPIKey.Load().(string); got != "exchanged-key" {
		t.Errorf("upstream X-API-Key = %q, want exchanged-key", got)
	}
	if n := d.tokenCalls.Load(); n != 1 {
		t.Errorf("token exchanges = %d, want 1", n)
	}

	// A second request reuses the cached key — no second exchange.
	rec = get(t, h, "/api/telemetry/sales/ordersync/traces/some-trace-id")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n := d.tokenCalls.Load(); n != 1 {
		t.Errorf("token exchanges after detail = %d, want 1 (cached)", n)
	}
	if n := d.traceCalls.Load(); n != 2 {
		t.Errorf("trace calls = %d, want 2", n)
	}
}

// oldStubDashboard mimics a pre-13.2 dashboard: the token exchange endpoint
// does not exist and the frontend redirects API-looking requests to /login.
func oldStubDashboard(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login?returnUrl="+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProxyTelemetryUnsupportedDashboard(t *testing.T) {
	root := t.TempDir()
	h, api, err := newHandler(root, "test", providers{topology: emptyTopo, deploy: emptyDeploy})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	old := oldStubDashboard(t)
	run := &systemRun{handle: &runHandle{pump: &logPump{}}}
	run.handle.pump.add("Login to the dashboard at " + old.URL + "/login?t=the-token")
	api.runMu.Lock()
	api.runs = map[string]*systemRun{"sales/ordersync": run}
	api.runMu.Unlock()

	telemetryMu.Lock()
	delete(upstreamCache, "sales/ordersync")
	telemetryMu.Unlock()
	t.Cleanup(func() {
		telemetryMu.Lock()
		delete(upstreamCache, "sales/ordersync")
		telemetryMu.Unlock()
	})

	for i := 0; i < 2; i++ {
		rec := get(t, h, "/api/telemetry/sales/ordersync/traces")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d: status = %d, want 503", i+1, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "telemetry API") {
			t.Errorf("request %d: body should name the missing telemetry API, got %s", i+1, rec.Body.String())
		}
	}
}

func TestProxyTelemetryNoDashboardInLogs(t *testing.T) {
	root := t.TempDir()
	h, api, err := newHandler(root, "test", providers{topology: emptyTopo, deploy: emptyDeploy})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	run := &systemRun{handle: &runHandle{pump: &logPump{}}}
	run.handle.pump.add("Starting without any aspire banner")
	api.runMu.Lock()
	api.runs = map[string]*systemRun{".": run}
	api.runMu.Unlock()

	// The workspace-root system's identifier is "." — a URL segment that
	// ServeMux cleans into a redirect, so the request arrives with an empty
	// wildcard and the handler treats it as the root system.
	req := httptest.NewRequest(http.MethodGet, "/api/telemetry/traces", nil)
	req.SetPathValue("path", "./traces")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "dashboard") {
		t.Errorf("body should name the missing dashboard, got %s", rec.Body.String())
	}
}
