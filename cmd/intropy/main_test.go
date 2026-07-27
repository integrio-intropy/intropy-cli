package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/command"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"runtime error", errors.New("something broke"), 1},
		{"usage error", newUsageErrorf("bad flag"), 2},
		{"cobra usage error", errors.New("unknown flag: --nope"), 2},
		{
			// A missing dependency is "command not found", which scripts and CI
			// can react to differently from a genuine failure.
			name: "missing binary",
			err:  &command.NotInstalledError{Binary: "kustomize"},
			want: 127,
		},
		{
			// Wrapping must survive: the error reaches main through several
			// layers of fmt.Errorf.
			name: "wrapped missing binary",
			err:  fmt.Errorf("deploy: %w", &command.NotInstalledError{Binary: "git"}),
			want: 127,
		},
		{
			// Ctrl-C during the ArgoCD wait or a push must look like a signal,
			// not like the deploy failing.
			name: "interrupted",
			err:  context.Canceled,
			want: 130,
		},
		{
			name: "wrapped interruption",
			err:  fmt.Errorf("resolve HEAD: %w", context.Canceled),
			want: 130,
		},
		{
			// A usage error takes precedence over anything it happens to wrap.
			name: "usage error wins over cause",
			err:  &usageError{err: fmt.Errorf("bad input: %w", context.Canceled)},
			want: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
