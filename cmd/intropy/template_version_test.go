package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestResolveTemplateVersion(t *testing.T) {
	// Cobra keeps parse state (including Flag.Changed) on the shared command
	// objects between Execute calls, so each subtest must clear it explicitly
	// or one subtest's --version leaks into the next.
	resetDescribe := func(t *testing.T) {
		t.Helper()
		intDescribeFlags = describeFlags{output: "plain"}
		intDescribeCmd.Flags().Lookup("version").Changed = false
		intDescribeCmd.Flags().Lookup("template-version").Changed = false
		t.Cleanup(func() {
			intDescribeFlags = describeFlags{output: "plain"}
			intDescribeCmd.Flags().Lookup("version").Changed = false
			intDescribeCmd.Flags().Lookup("template-version").Changed = false
		})
	}

	t.Run("canonical flag alone wins silently", func(t *testing.T) {
		resetDescribe(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)

		rootCmd.SetArgs([]string{"int", "describe", "hello-world", "--template-version", "v1.2.3"})
		_ = rootCmd.Execute() // network error expected; flag wiring is what we assert
		if intDescribeFlags.templateVersion != "v1.2.3" {
			t.Errorf("templateVersion = %q, want v1.2.3", intDescribeFlags.templateVersion)
		}
		if strings.Contains(stderr.String(), "deprecated") {
			t.Errorf("canonical flag must not warn, got: %q", stderr.String())
		}
	})

	t.Run("deprecated alias warns and copies", func(t *testing.T) {
		resetDescribe(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)

		rootCmd.SetArgs([]string{"int", "describe", "hello-world", "--version", "v1.2.3"})
		_ = rootCmd.Execute()
		if intDescribeFlags.templateVersion != "v1.2.3" {
			t.Errorf("templateVersion = %q, want alias copy v1.2.3", intDescribeFlags.templateVersion)
		}
		if !strings.Contains(stderr.String(), "--version is deprecated") {
			t.Errorf("expected deprecation warning, got: %q", stderr.String())
		}
	})

	t.Run("both flags with different values are a usage error", func(t *testing.T) {
		resetDescribe(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)

		rootCmd.SetArgs([]string{"int", "describe", "hello-world", "--version", "v1.0.0", "--template-version", "v2.0.0"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
		if !strings.Contains(err.Error(), "cannot combine") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("both flags with the same value warn but pass", func(t *testing.T) {
		resetDescribe(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)

		rootCmd.SetArgs([]string{"int", "describe", "hello-world", "--version", "v1.2.3", "--template-version", "v1.2.3"})
		_ = rootCmd.Execute()
		if !strings.Contains(stderr.String(), "--version is deprecated") {
			t.Errorf("expected deprecation warning even when values match, got: %q", stderr.String())
		}
	})

	t.Run("alias is hidden from help", func(t *testing.T) {
		for _, path := range [][]string{
			{"int", "create"},
			{"int", "describe"},
			{"sys", "create"},
			{"deploy", "init"},
		} {
			cmd, _, err := rootCmd.Find(path)
			if err != nil || cmd == nil {
				t.Fatalf("could not find %v: %v", path, err)
			}
			f := cmd.Flags().Lookup("version")
			if f == nil {
				t.Errorf("%v: --version alias missing", path)
				continue
			}
			if !f.Hidden {
				t.Errorf("%v: deprecated --version alias must be hidden", path)
			}
			if cmd.Flags().Lookup("template-version") == nil {
				t.Errorf("%v: --template-version missing", path)
			}
		}
	})
}
