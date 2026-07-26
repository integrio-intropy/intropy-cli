package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// statusClient points a registry Client at a server that answers every
// request with the given status.
func statusClient(t *testing.T, status int) (*Client, string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"errors":[{"code":"DENIED","message":"denied"}]}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(
		WithCredentials(anonymous),
		WithHTTPClient(srv.Client()),
		WithPlainHTTP(func(string) bool { return true }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, strings.TrimPrefix(srv.URL, "http://")
}

func TestResolveMapsUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, host := statusClient(t, status)

			_, err := c.Resolve(context.Background(), host+"/releases/component-x:1.4.2")
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Resolve error = %v; want ErrUnauthorized", err)
			}
			if !strings.Contains(err.Error(), "docker login "+host) {
				t.Errorf("error %q does not tell the user to run 'docker login %s'", err, host)
			}
		})
	}
}

func TestResolveMapsNotFound(t *testing.T) {
	c, host := statusClient(t, http.StatusNotFound)

	_, err := c.Resolve(context.Background(), host+"/releases/component-x:1.4.2")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve error = %v; want ErrNotFound", err)
	}
}

func TestResolvePassesThroughOtherErrors(t *testing.T) {
	c, host := statusClient(t, http.StatusInternalServerError)

	_, err := c.Resolve(context.Background(), host+"/releases/component-x:1.4.2")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnauthorized) {
		t.Errorf("500 was mapped to a sentinel error: %v", err)
	}
}

func TestPullArtifactMapsUnauthorized(t *testing.T) {
	c, host := statusClient(t, http.StatusUnauthorized)

	_, _, err := c.PullArtifact(context.Background(), host+"/releases/component-x:1.4.2")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("PullArtifact error = %v; want ErrUnauthorized", err)
	}
}

func TestPushArtifactMapsUnauthorized(t *testing.T) {
	c, host := statusClient(t, http.StatusUnauthorized)

	_, err := c.PushArtifact(context.Background(), host+"/releases/component-x:1.4.2", testArtifact())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("PushArtifact error = %v; want ErrUnauthorized", err)
	}
}

func TestPullIndexMapsNotFound(t *testing.T) {
	c, host := statusClient(t, http.StatusNotFound)

	_, _, err := c.PullIndex(context.Background(), host+"/skills/index:latest")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("PullIndex error = %v; want ErrNotFound", err)
	}
}
