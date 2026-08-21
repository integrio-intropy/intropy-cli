package argocd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	previousRevision = "1111111111111111111111111111111111111111"
	ourRevision      = "2222222222222222222222222222222222222222"
	laterRevision    = "3333333333333333333333333333333333333333"
)

// appState is one scripted answer from the fake ArgoCD.
type appState struct {
	sync     string
	revision string
	health   string
	phase    string
	message  string
}

// fakeArgo serves a scripted sequence of application states: each GET returns
// the next one, and the last repeats.
type fakeArgo struct {
	*httptest.Server
	states    []appState
	gets      atomic.Int32
	refreshes atomic.Int32
	events    []Event
	lastQuery atomic.Value
}

func newFakeArgo(t *testing.T, states ...appState) *fakeArgo {
	t.Helper()
	f := &fakeArgo{states: states}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/applications/", func(w http.ResponseWriter, r *http.Request) {
		f.lastQuery.Store(r.URL.RawQuery)

		if strings.HasSuffix(r.URL.Path, "/events") {
			writeJSON(t, w, map[string]any{"items": f.events})
			return
		}
		if r.URL.Query().Get("refresh") != "" {
			f.refreshes.Add(1)
		}

		i := int(f.gets.Add(1)) - 1
		if i >= len(f.states) {
			i = len(f.states) - 1
		}
		st := f.states[i]

		var app Application
		app.Metadata.Name = "orders-order-flow-order-extractor-dev"
		app.Status.Sync.Status = st.sync
		app.Status.Sync.Revision = st.revision
		app.Status.Health.Status = st.health
		app.Status.OperationState.Phase = st.phase
		app.Status.OperationState.Message = st.message
		writeJSON(t, w, app)
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeArgo) client(t *testing.T, appNamespace string) *Client {
	t.Helper()
	c, err := NewClient(Options{
		Credentials:  Credentials{Server: strings.TrimPrefix(f.URL, "http://"), PlainText: true},
		AppNamespace: appNamespace,
		HTTP:         f.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fastWait(revision string) WaitOptions {
	return WaitOptions{
		App:          "orders-order-flow-order-extractor-dev",
		Revision:     revision,
		Timeout:      2 * time.Second,
		PollInterval: time.Millisecond,
	}
}

// The bug every implementation of this ships first: the previous revision is
// already Synced and Healthy, so polling on status alone declares success
// before ArgoCD has done anything at all.
func TestWaitIgnoresPreviousRevisionAlreadyHealthy(t *testing.T) {
	f := newFakeArgo(t,
		appState{sync: SyncSynced, revision: previousRevision, health: HealthHealthy},
		appState{sync: "OutOfSync", revision: previousRevision, health: HealthHealthy},
		appState{sync: SyncSynced, revision: ourRevision, health: "Progressing"},
		appState{sync: SyncSynced, revision: ourRevision, health: HealthHealthy},
	)

	app, err := f.client(t, "customer-fluxia").Wait(context.Background(), fastWait(ourRevision))
	if err != nil {
		t.Fatal(err)
	}
	if app.Status.Sync.Revision != ourRevision {
		t.Errorf("returned revision = %q, want ours", app.Status.Sync.Revision)
	}
	// It must have kept polling past the first, already-healthy answer.
	if n := f.gets.Load(); n < 4 {
		t.Errorf("only %d polls; it stopped before our revision was applied", n)
	}
}

// Another deployment landing after ours makes ArgoCD sync a descendant commit,
// so our sha never appears on its own. Strict equality would hang forever.
func TestWaitAcceptsADescendantRevision(t *testing.T) {
	f := newFakeArgo(t,
		appState{sync: SyncSynced, revision: previousRevision, health: HealthHealthy},
		appState{sync: SyncSynced, revision: laterRevision, health: HealthHealthy},
	)

	opts := fastWait(ourRevision)
	// laterRevision contains ourRevision.
	opts.Contains = func(_ context.Context, mine, reported string) (bool, error) {
		return reported == mine || (mine == ourRevision && reported == laterRevision), nil
	}

	app, err := f.client(t, "customer-fluxia").Wait(context.Background(), opts)
	if err != nil {
		t.Fatalf("a descendant revision should satisfy the wait: %v", err)
	}
	if app.Status.Sync.Revision != laterRevision {
		t.Errorf("returned revision = %q, want the descendant", app.Status.Sync.Revision)
	}
}

// Without a Contains function only exact equality counts, and a descendant must
// therefore time out rather than be silently accepted.
func TestWaitDefaultsToExactEquality(t *testing.T) {
	f := newFakeArgo(t, appState{sync: SyncSynced, revision: laterRevision, health: HealthHealthy})

	opts := fastWait(ourRevision)
	opts.Timeout = 150 * time.Millisecond

	_, err := f.client(t, "customer-fluxia").Wait(context.Background(), opts)
	if _, ok := errors.AsType[*TimeoutError](err); !ok {
		t.Fatalf("error %v should be *TimeoutError", err)
	}
}

func TestWaitTimeoutReportsLastStateAndEvents(t *testing.T) {
	f := newFakeArgo(t, appState{
		sync: "OutOfSync", revision: previousRevision, health: HealthDegraded,
		phase: "Running", message: "waiting for healthy state of batch/CronJob/order-extractor",
	})
	f.events = []Event{
		{Type: "Normal", Reason: "ResourceUpdated", Message: "updated CronJob"},
		{Type: "Warning", Reason: "SyncFailed", Message: "ImagePullBackOff"},
	}

	opts := fastWait(ourRevision)
	opts.Timeout = 150 * time.Millisecond

	_, err := f.client(t, "customer-fluxia").Wait(context.Background(), opts)
	timeout, ok := errors.AsType[*TimeoutError](err)
	if !ok {
		t.Fatalf("error %v should be *TimeoutError", err)
	}
	msg := timeout.Error()
	// Everything needed to diagnose it, without a second command.
	for _, want := range []string{"did not converge", "OutOfSync", HealthDegraded, "waiting for healthy state", "ImagePullBackOff"} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message should mention %q:\n%s", want, msg)
		}
	}
	// Warnings first: they explain the failure.
	if strings.Index(msg, "ImagePullBackOff") > strings.Index(msg, "updated CronJob") {
		t.Errorf("warnings should be listed before normal events:\n%s", msg)
	}
}

// A terminal phase will not improve by waiting, so it fails immediately rather
// than burning the whole timeout.
func TestWaitFailsFastOnTerminalPhase(t *testing.T) {
	f := newFakeArgo(t, appState{
		sync: SyncSynced, revision: ourRevision, health: HealthDegraded,
		phase: PhaseFailed, message: "one or more objects failed to apply",
	})

	start := time.Now()
	opts := fastWait(ourRevision)
	opts.Timeout = 10 * time.Second

	_, err := f.client(t, "customer-fluxia").Wait(context.Background(), opts)
	failed, ok := errors.AsType[*SyncFailedError](err)
	if !ok {
		t.Fatalf("error %v should be *SyncFailedError", err)
	}
	if !strings.Contains(failed.Error(), "failed to apply") {
		t.Errorf("error should carry ArgoCD's message: %v", failed)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %s; a terminal phase should not wait out the timeout", elapsed)
	}
}

// A terminal phase attached to the *previous* revision says nothing about ours,
// so it must not abort the wait.
func TestWaitIgnoresTerminalPhaseOnAnotherRevision(t *testing.T) {
	f := newFakeArgo(t,
		appState{sync: "OutOfSync", revision: previousRevision, health: HealthDegraded, phase: PhaseFailed, message: "an older failure"},
		appState{sync: SyncSynced, revision: ourRevision, health: HealthHealthy},
	)

	if _, err := f.client(t, "customer-fluxia").Wait(context.Background(), fastWait(ourRevision)); err != nil {
		t.Fatalf("a failure on an earlier revision should not abort the wait: %v", err)
	}
}

// Without a hard refresh the wait would sit through ArgoCD's own three-minute
// reconciliation interval before anything could change.
func TestWaitRefreshesFirst(t *testing.T) {
	f := newFakeArgo(t, appState{sync: SyncSynced, revision: ourRevision, health: HealthHealthy})
	if _, err := f.client(t, "customer-fluxia").Wait(context.Background(), fastWait(ourRevision)); err != nil {
		t.Fatal(err)
	}
	if n := f.refreshes.Load(); n != 1 {
		t.Errorf("refreshes = %d, want exactly 1", n)
	}
}

// appNamespace has to be on every request: Applications live per customer, and
// omitting it returns a 404 indistinguishable from a missing application.
func TestRequestsCarryAppNamespace(t *testing.T) {
	f := newFakeArgo(t, appState{sync: SyncSynced, revision: ourRevision, health: HealthHealthy})
	if _, err := f.client(t, "customer-fluxia").Wait(context.Background(), fastWait(ourRevision)); err != nil {
		t.Fatal(err)
	}
	if q, _ := f.lastQuery.Load().(string); !strings.Contains(q, "appNamespace=customer-fluxia") {
		t.Errorf("query %q should carry appNamespace", q)
	}
}

func TestWaitInterruptedReportsCancellation(t *testing.T) {
	f := newFakeArgo(t, appState{sync: "OutOfSync", revision: previousRevision, health: HealthHealthy})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := f.client(t, "customer-fluxia").Wait(ctx, fastWait(ourRevision))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v should be context.Canceled, so the exit code is 130 rather than a failure", err)
	}
}

func TestGetNotFoundNamesTheNamespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{
		Credentials:  Credentials{Server: strings.TrimPrefix(srv.URL, "http://"), PlainText: true},
		AppNamespace: "customer-fluxia",
		HTTP:         srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Get(context.Background(), "missing-app")
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("error %v should be ErrAppNotFound", err)
	}
	// The namespace is the far more common mistake than a genuinely absent app.
	if !strings.Contains(err.Error(), "customer-fluxia") || !strings.Contains(err.Error(), "appNamespace") {
		t.Errorf("error %q should point at appNamespace", err)
	}
}

// An expired token is reported as unreachable so a caller that has already
// pushed warns rather than claiming the deployment failed.
func TestUnauthorizedIsUnreachableWithLoginHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{
		Credentials: Credentials{Server: strings.TrimPrefix(srv.URL, "http://"), PlainText: true},
		HTTP:        srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "app")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("error %v should be ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), "argocd login") {
		t.Errorf("error %q should tell the user to log in", err)
	}
}

// ArgoCD answers 403 rather than 404 for an application the caller may not
// see, so the response cannot leak whether it exists. Either way the
// application is not readable: the caller treats it as not found, not as an
// authentication failure — the token was accepted.
func TestForbiddenIsAppNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{
		Credentials: Credentials{Server: strings.TrimPrefix(srv.URL, "http://"), PlainText: true},
		HTTP:        srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "app")
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("error %v should be ErrAppNotFound", err)
	}
	if strings.Contains(err.Error(), "argocd login") {
		t.Errorf("error %q must not send the user to re-login for an accepted token", err)
	}
}

func TestConnectionFailureIsUnreachable(t *testing.T) {
	c, err := NewClient(Options{
		// Port 1 is reserved and nothing listens on it.
		Credentials: Credentials{Server: "127.0.0.1:1", PlainText: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "app"); !errors.Is(err, ErrUnreachable) {
		t.Errorf("error %v should be ErrUnreachable", err)
	}
}

func TestApplicationSynced(t *testing.T) {
	var app Application
	app.Status.Sync.Status = SyncSynced
	app.Status.Health.Status = HealthHealthy
	if !app.Synced() {
		t.Error("Synced+Healthy should report synced")
	}
	app.Status.Health.Status = HealthDegraded
	if app.Synced() {
		t.Error("Degraded should not report synced")
	}
	app.Status.Sync.Status = "OutOfSync"
	app.Status.Health.Status = HealthHealthy
	if app.Synced() {
		t.Error("OutOfSync should not report synced")
	}
}
