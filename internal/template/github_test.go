package template

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

// TestLatestTagReportsRateLimits pins the three shapes GitHub spends on a
// limit, and that an ordinary 403 stays an ordinary 403 — a permissions
// failure reported as a rate limit would send the user to wait for nothing.
func TestLatestTagReportsRateLimits(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(90*time.Second).Unix(), 10)
	tests := []struct {
		name      string
		status    int
		headers   map[string]string
		body      string
		want      string
		secondary bool
		plain     bool
	}{
		{
			name:      "secondary limit names its wait",
			status:    http.StatusForbidden,
			headers:   map[string]string{"Retry-After": "60"},
			body:      `{"message":"You have exceeded a secondary rate limit"}`,
			want:      "github secondary rate limit reached; retry in 1m0s",
			secondary: true,
		},
		{
			name:    "exhausted hourly budget names its reset",
			status:  http.StatusForbidden,
			headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset},
			body:    `{"message":"API rate limit exceeded"}`,
			want:    "github rate limit reached; retry in 1m", // exact seconds depend on clock skew
		},
		{
			name:      "429 without a named wait still reports the limit",
			status:    http.StatusTooManyRequests,
			body:      `{"message":"Too many requests"}`,
			want:      "github rate limit reached",
			secondary: false,
		},
		{
			name:   "plain 403 is not a rate limit",
			status: http.StatusForbidden,
			body:   `{"message":"Must have admin rights"}`,
			want:   "Must have admin rights",
			plain:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			gh := NewGitHubClient(srv.Client(), "test", srv.URL)
			_, err := gh.LatestTag(context.Background(), "o", "r")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}

			var limit *RateLimitError
			switch got := errors.As(err, &limit); {
			case tc.plain && got:
				t.Errorf("plain %d reported as a rate limit: %v", tc.status, err)
			case !tc.plain && !got:
				t.Errorf("rate limit not typed as RateLimitError: %v", err)
			case got && limit.Secondary != tc.secondary:
				t.Errorf("Secondary = %v, want %v", limit.Secondary, tc.secondary)
			}
		})
	}
}
