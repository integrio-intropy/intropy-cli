package main

import (
	"os"
	"strings"
)

func main() {
	if err := Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

// exitCode maps Cobra/user errors to Unix-style exit codes.
// Cobra does not expose one usage-error type, so common flag and argument
// errors are classified by message prefix.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if isUsageError(err.Error()) {
		return 2
	}
	return 1
}

func isUsageError(msg string) bool {
	usagePrefixes := []string{
		"unknown command",
		"unknown flag",
		"invalid argument",
		"accepts ",
		"requires ",
		"required flag(s)",
	}
	for _, prefix := range usagePrefixes {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}
