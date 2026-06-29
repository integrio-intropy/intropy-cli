package main

import (
	"errors"
	"os"
	"strings"
)

func main() {
	if err := Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

// exitCode maps errors to Unix-style exit codes.
//
//	0 — success
//	1 — runtime error
//	2 — usage error (invalid flags, arguments, or missing required input)
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	// Cobra's built-in flag/argument errors don't wrap a typed error,
	// so we fall back to message prefix detection for those.
	if isCobraUsageError(err) {
		return 2
	}
	return 1
}

// isCobraUsageError detects Cobra-generated flag and argument errors
// that should map to exit code 2. These are errors from Cobra itself,
// not from our RunE functions (which should use usageError).
func isCobraUsageError(err error) bool {
	msg := err.Error()
	prefixes := []string{
		"unknown command",
		"unknown flag",
		"invalid argument",
		"accepts ",
		"requires ",
		"required flag(s)",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}
