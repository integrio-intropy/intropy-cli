package argocd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// syncRecorder captures the one POST a sync makes.
type syncRecorder struct {
	*httptest.Server
	method      string
	path        string
	rawQuery    string
	contentType string
	body        map[string]any
	status      int
}

func newSyncRecorder(t *testing.T, appNamespace string) (*syncRecorder, *Client) {
	t.Helper()
	rec := &syncRecorder{status: http.StatusOK}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.rawQuery = r.Method, r.URL.Path, r.URL.RawQuery
		rec.contentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&rec.body); err != nil {
			t.Errorf("decode sync request: %v", err)
		}
		if rec.status != http.StatusOK {
			w.WriteHeader(rec.status)
			return
		}
		writeJSON(t, w, map[string]any{})
	}))
	t.Cleanup(rec.Close)

	client, err := NewClient(Options{
		Credentials:  Credentials{Server: strings.TrimPrefix(rec.URL, "http://"), PlainText: true},
		AppNamespace: appNamespace,
		HTTP:         rec.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec, client
}

// The whole point of the call: ArgoCD must be told which revision to apply, not
// left to pick up whatever the branch holds when it next reconciles.
func TestSyncTargetsTheGivenRevision(t *testing.T) {
	rec, client := newSyncRecorder(t, "customer-fluxia")

	if err := client.Sync(context.Background(), "orders-order-flow-order-extractor-prod", ourRevision); err != nil {
		t.Fatal(err)
	}

	if rec.method != http.MethodPost {
		t.Errorf("method = %s, want POST", rec.method)
	}
	if want := "/api/v1/applications/orders-order-flow-order-extractor-prod/sync"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", rec.contentType)
	}
	if got := rec.body["revision"]; got != ourRevision {
		t.Errorf("revision = %v, want %s", got, ourRevision)
	}
	// Pruning deletes resources, which is a much larger decision than applying a
	// reviewed image pin. A sync from here must not make it silently.
	if got := rec.body["prune"]; got != false {
		t.Errorf("prune = %v, want false", got)
	}
	if got := rec.body["dryRun"]; got != false {
		t.Errorf("dryRun = %v, want false", got)
	}
}

// Applications are deployed per customer rather than into the argocd namespace,
// and ArgoCD defines appNamespace in this endpoint's *body*. Sending it only as
// a query parameter would 404 in a way indistinguishable from a missing
// application.
func TestSyncSendsAppNamespaceInBodyAndQuery(t *testing.T) {
	rec, client := newSyncRecorder(t, "customer-fluxia")

	if err := client.Sync(context.Background(), "app", ourRevision); err != nil {
		t.Fatal(err)
	}

	if got := rec.body["appNamespace"]; got != "customer-fluxia" {
		t.Errorf("body appNamespace = %v, want customer-fluxia", got)
	}
	if !strings.Contains(rec.rawQuery, "appNamespace=customer-fluxia") {
		t.Errorf("query = %q, want it to carry appNamespace", rec.rawQuery)
	}
}

func TestSyncOmitsAnEmptyAppNamespace(t *testing.T) {
	rec, client := newSyncRecorder(t, "")

	if err := client.Sync(context.Background(), "app", ourRevision); err != nil {
		t.Fatal(err)
	}

	if got, found := rec.body["appNamespace"]; found {
		t.Errorf("appNamespace = %v, want it absent", got)
	}
}

// A rejected token is the common failure, and the message has to say what to do
// about it rather than surface a raw 401.
func TestSyncUnauthorizedIsUnreachable(t *testing.T) {
	rec, client := newSyncRecorder(t, "customer-fluxia")
	rec.status = http.StatusUnauthorized

	err := client.Sync(context.Background(), "app", ourRevision)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("error = %v, want it to wrap ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), "argocd login") {
		t.Errorf("error %q should say how to get a token", err)
	}
}

// RBAC denies the sync: the caller lacks prod rights. That is a real failure and
// must not be softened into a warning the way an unreachable server is for a
// deploy that has already pushed.
func TestSyncForbiddenFails(t *testing.T) {
	rec, client := newSyncRecorder(t, "customer-fluxia")
	rec.status = http.StatusForbidden

	if err := client.Sync(context.Background(), "app", ourRevision); err == nil {
		t.Fatal("a forbidden sync must fail")
	}
}
