package oci

import "testing"

func TestParseReference(t *testing.T) {
	cases := []struct {
		in   string
		want Reference
	}{
		{
			in:   "ghcr.io/example/skills/pr-review:1.2.0",
			want: Reference{Registry: "ghcr.io", Repository: "example/skills/pr-review", Tag: "1.2.0"},
		},
		{
			in:   "registry.local:5000/skills/foo:0.1.0",
			want: Reference{Registry: "registry.local:5000", Repository: "skills/foo", Tag: "0.1.0"},
		},
		{
			in:   "ghcr.io/example/skills/pr-review@sha256:abc123",
			want: Reference{Registry: "ghcr.io", Repository: "example/skills/pr-review", Digest: "sha256:abc123"},
		},
		{
			in:   "ghcr.io/example/skills/pr-review:1.2.0@sha256:abc123",
			want: Reference{Registry: "ghcr.io", Repository: "example/skills/pr-review", Tag: "1.2.0", Digest: "sha256:abc123"},
		},
		{
			in:   "ghcr.io/example/skills/pr-review",
			want: Reference{Registry: "ghcr.io", Repository: "example/skills/pr-review"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseReference(tc.in)
			if err != nil {
				t.Fatalf("ParseReference(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseReference(%q) = %#v; want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseReferenceErrors(t *testing.T) {
	if _, err := ParseReference("no-slash-anywhere"); err == nil {
		t.Errorf("expected error for reference without registry/repo separator")
	}
}
