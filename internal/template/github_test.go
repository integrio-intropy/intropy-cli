package template

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tag_name":"v1.2.3"}`)
	}))
	defer srv.Close()

	g := newGitHub(srv.Client(), "test")
	g.BaseURL = srv.URL

	tag, err := g.LatestTag(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.2.3" {
		t.Errorf("tag = %q", tag)
	}
}

func TestLatestTagHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	g := newGitHub(srv.Client(), "test")
	g.BaseURL = srv.URL
	_, err := g.LatestTag(context.Background(), "o", "r")
	if err == nil {
		t.Fatal("expected error")
	}
}
