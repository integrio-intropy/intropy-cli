package template

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newListServer serves a release endpoint and a contents endpoint with a
// fixed set of repository root entries. gotRef records the ?ref= query the
// client sent, so tests can pin that a resolved tag is passed through.
func newListServer(t *testing.T, tag string, gotRef *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/repos/o/r/contents/", func(w http.ResponseWriter, r *http.Request) {
		*gotRef = r.URL.Query().Get("ref")
		w.Header().Set("Content-Type", "application/json")
		entries := []map[string]string{
			{"name": "README.md", "type": "file"},
			{"name": "transactional", "type": "dir"},
			{"name": "hello-world", "type": "dir"},
			{"name": ".github", "type": "dir"},
		}
		_ = json.NewEncoder(w).Encode(entries)
	})
	return httptest.NewServer(mux)
}

func TestList(t *testing.T) {
	var gotRef string
	srv := newListServer(t, "v1.2.3", &gotRef)
	defer srv.Close()

	got, err := List(context.Background(), ListOptions{
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got.Version)
	}
	if gotRef != "v1.2.3" {
		t.Errorf("contents request ref = %q, want the resolved tag v1.2.3", gotRef)
	}
	// Files are excluded, and names come back sorted.
	want := []string{".github", "hello-world", "transactional"}
	if len(got.Templates) != len(want) {
		t.Fatalf("Templates = %v, want %v", got.Templates, want)
	}
	for i, name := range want {
		if got.Templates[i] != name {
			t.Errorf("Templates[%d] = %q, want %q", i, got.Templates[i], name)
		}
	}
}

func TestListPinnedVersionSkipsReleaseLookup(t *testing.T) {
	var gotRef string
	srv := newListServer(t, "unused", &gotRef)
	defer srv.Close()

	got, err := List(context.Background(), ListOptions{
		Version:       "v9.9.9",
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.Version != "v9.9.9" {
		t.Errorf("Version = %q, want v9.9.9", got.Version)
	}
	if gotRef != "v9.9.9" {
		t.Errorf("contents request ref = %q, want v9.9.9", gotRef)
	}
}
