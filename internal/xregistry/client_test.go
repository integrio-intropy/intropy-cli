package xregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, w http.ResponseWriter, doc any) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func TestRegistryErrorBodySurfaced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(t, w, map[string]any{"code": "unavailable", "detail": "registry warming up", "status": 503})
	})
	tsvc := httptest.NewServer(mux)
	t.Cleanup(tsvc.Close)
	c, err := New(tsvc.URL, WithUserAgent("test"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Export(context.Background())
	if err == nil {
		t.Fatal("expected an error for the registry response")
	}
	for _, want := range []string{"unavailable", "registry warming up"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should surface the registry body's %q", err, want)
		}
	}
}

func TestUnreachableHostIsWrappedNotPanicked(t *testing.T) {
	c, err := New("http://127.0.0.1:1", WithUserAgent("test"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Export(context.Background())
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if !strings.Contains(err.Error(), "fetch xRegistry export") || !strings.Contains(err.Error(), "GET") {
		t.Errorf("error %q should wrap the operation, not panic", err)
	}
}

func TestNewDefaultHTTPClientHasTimeout(t *testing.T) {
	c, err := New("https://registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient.Timeout != defaultHTTPTimeout {
		t.Errorf("timeout = %v, want %v", c.httpClient.Timeout, defaultHTTPTimeout)
	}

	custom := &http.Client{}
	c, err = New("https://registry.example.com", WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient != custom {
		t.Error("WithHTTPClient should keep the caller-provided client")
	}
}

func TestNewRejectsNonAbsoluteBaseURL(t *testing.T) {
	if _, err := New("not-a-url"); err == nil {
		t.Error("New should reject a relative base URL")
	}
}
