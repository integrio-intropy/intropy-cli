package template

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestTarball(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/o/r/tarball/") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") != "test" {
			t.Errorf("missing User-Agent header")
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	g := newGitHub(srv.Client(), "test")
	g.BaseURL = srv.URL

	rc, err := g.Tarball(context.Background(), "o", "r", "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Errorf("body = %q", string(b))
	}
}

func TestTarballHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()
	g := newGitHub(srv.Client(), "test")
	g.BaseURL = srv.URL
	if _, err := g.Tarball(context.Background(), "o", "r", "v1"); err == nil {
		t.Fatal("expected error")
	}
}
