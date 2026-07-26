package registry

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestIsLocalRegistry(t *testing.T) {
	cases := []struct {
		registry string
		want     bool
	}{
		{registry: "localhost", want: true},
		{registry: "localhost:5000", want: true},
		{registry: "127.0.0.1", want: true},
		{registry: "127.0.0.1:5000", want: true},
		{registry: "[::1]:5000", want: true},
		{registry: "ghcr.io", want: false},
		{registry: "harbor.intropy.io", want: false},
		{registry: "registry.local:5000", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.registry, func(t *testing.T) {
			if got := isLocalRegistry(tc.registry); got != tc.want {
				t.Errorf("isLocalRegistry(%q) = %v; want %v", tc.registry, got, tc.want)
			}
		})
	}
}

func TestClientDefaultsToLocalPlainHTTP(t *testing.T) {
	c, err := NewClient(WithCredentials(anonymous))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	local, err := c.repository(Reference{Registry: "localhost:5000", Repository: "skills/foo"})
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	if !local.PlainHTTP {
		t.Error("expected PlainHTTP for localhost registry")
	}

	remote, err := c.repository(Reference{Registry: "harbor.intropy.io", Repository: "skills/foo"})
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	if remote.PlainHTTP {
		t.Error("expected TLS for a non-local registry")
	}
}

func TestWithPlainHTTPOverride(t *testing.T) {
	c, err := NewClient(
		WithCredentials(anonymous),
		WithPlainHTTP(func(string) bool { return true }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	repo, err := c.repository(Reference{Registry: "harbor.intropy.io", Repository: "skills/foo"})
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	if !repo.PlainHTTP {
		t.Error("expected WithPlainHTTP override to apply to a remote registry")
	}
}

func anonymous(context.Context, string) (auth.Credential, error) {
	return auth.EmptyCredential, nil
}
