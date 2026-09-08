package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/command"
	"github.com/spf13/cobra"
)

func main() {
	if err := Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

// exitCode maps errors to Unix-style exit codes.
//
//	0   — success
//	1   — runtime error
//	2   — usage error (invalid flags, arguments, or missing required input)
//	127 — a required external binary is missing from PATH
//	130 — interrupted (SIGINT)
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	// Unknown commands surface from cobra's Find before any command in the
	// tree runs, so they fall back to message prefix detection.
	if isCobraUsageError(err) {
		return 2
	}
	// A missing dependency is "command not found", not a generic failure —
	// scripts and CI can tell the two apart and react differently.
	if errors.Is(err, command.ErrNotInstalled) {
		return 127
	}
	// Ctrl-C during a long operation (the ArgoCD wait, a push over SSH) must
	// look like a signal, not a failure of the thing being run.
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}

// usageArgs wraps a Cobra arg-count validator so its failure is a typed
// usageError rather than an untyped message exitCode would have to sniff.
// The returned validator refuses to wrap an error that already carries a
// usageError, so it stays a no-op if it is ever applied twice (test-global
// cobra state makes double-wrapping hard to rule out statically).
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			var ue *usageError
			if errors.As(err, &ue) {
				return err
			}
			return &usageError{err: err}
		}
		return nil
	}
}

// isCobraUsageError detects unknown commands, the one Cobra error class
// that carries no typed marker and escapes the wrapping in rune.go and
// usageArgs: cobra's Find fails before any command in the tree runs.
func isCobraUsageError(err error) bool {
	msg := err.Error()
	prefixes := []string{
		"unknown command",
		"unknown flag",
		"unknown shorthand flag",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}
