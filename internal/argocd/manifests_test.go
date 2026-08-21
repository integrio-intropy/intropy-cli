package argocd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// manifestRecorder captures the one GET a render makes.
type manifestRecorder struct {
	*httptest.Server
	method   string
	path     string
	rawQuery string
	status   int
}

func newManifestRecorder(t *testing.T, appNamespace string) (*manifestRecorder, *Client) {
	t.Helper()
	rec := &manifestRecorder{status: http.StatusOK}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.rawQuery = r.Method, r.URL.Path, r.URL.RawQuery
		if rec.status != http.StatusOK {
			w.WriteHeader(rec.status)
			return
		}
		writeJSON(t, w, map[string]any{
			"manifests": []string{`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings"}}`},
			"revision":  ourRevision,
		})
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

// The revision is the whole point: this endpoint is asked for a specific commit's
// render so two of them can be compared.
func TestManifestsRendersTheGivenRevision(t *testing.T) {
	rec, client := newManifestRecorder(t, "customer-fluxia")

	res, err := client.Manifests(context.Background(), "orders-order-flow-order-extractor-prod", ourRevision)
	if err != nil {
		t.Fatal(err)
	}

	if rec.method != http.MethodGet {
		t.Errorf("method = %s, want GET", rec.method)
	}
	if want := "/api/v1/applications/orders-order-flow-order-extractor-prod/manifests"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if !strings.Contains(rec.rawQuery, "revision="+ourRevision) {
		t.Errorf("query = %q, should carry the revision", rec.rawQuery)
	}
	// Applications live per customer, so the namespace has to travel with the
	// request or this 404s.
	if !strings.Contains(rec.rawQuery, "appNamespace=customer-fluxia") {
		t.Errorf("query = %q, should carry the appNamespace", rec.rawQuery)
	}

	if len(res.Manifests) != 1 || !strings.Contains(res.Manifests[0], "ConfigMap") {
		t.Errorf("manifests = %v, want the one rendered resource", res.Manifests)
	}
	if res.Revision != ourRevision {
		t.Errorf("revision = %q, want the revision ArgoCD resolved", res.Revision)
	}
}

// An empty revision is read by ArgoCD as "whatever the branch holds", which for a
// caller comparing two revisions silently renders the same tree twice. Refused
// here rather than at every call site.
func TestManifestsRefusesAnEmptyRevision(t *testing.T) {
	rec, client := newManifestRecorder(t, "")

	if _, err := client.Manifests(context.Background(), "app", ""); err == nil {
		t.Fatal("an empty revision must be refused")
	}
	if rec.method != "" {
		t.Errorf("no request should have been made, got %s %s", rec.method, rec.path)
	}
}

func TestManifestsNotFoundExplainsTheNamespace(t *testing.T) {
	rec, client := newManifestRecorder(t, "customer-fluxia")
	rec.status = http.StatusNotFound

	_, err := client.Manifests(context.Background(), "app", ourRevision)
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("error = %v, want ErrAppNotFound", err)
	}
	if !strings.Contains(err.Error(), "customer-fluxia") {
		t.Errorf("the error should name the namespace tried: %v", err)
	}
}

func TestManifestsUnauthorizedIsUnreachable(t *testing.T) {
	rec, client := newManifestRecorder(t, "")
	rec.status = http.StatusUnauthorized

	_, err := client.Manifests(context.Background(), "app", ourRevision)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("error = %v, want ErrUnreachable", err)
	}
}
