package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSysCreateRequiresName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resetRootIO(t, &stdout, &stderr)
	t.Chdir(t.TempDir())

	rootCmd.SetArgs([]string{"sys", "create"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"name"`) {
		t.Fatalf("err = %v, want required-flag error for name", err)
	}
	if exitCode(err) != 2 {
		t.Errorf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestSysCreateRejectsSetFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resetRootIO(t, &stdout, &stderr)
	t.Chdir(t.TempDir())

	rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--set", "foo=bar"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want unknown flag (sys create renders with only the name)", err)
	}
	if exitCode(err) != 2 {
		t.Errorf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestSysCreateDeprecatedOutputAlias(t *testing.T) {
	resetFlags := func(t *testing.T) {
		t.Helper()
		sysCreateFlagValues = sysCreateFlags{}
		t.Cleanup(func() { sysCreateFlagValues = sysCreateFlags{} })
	}

	t.Run("alias alone warns and copies to outDir", func(t *testing.T) {
		resetFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--output", "./host"})
		err := rootCmd.Execute()
		// Expect a scaffold-discovery error, not a flag error.
		if err != nil && strings.Contains(err.Error(), "required") {
			t.Errorf("deprecated --output should not trigger a required-flag error: %v", err)
		}
		if !strings.Contains(stderr.String(), "deprecated") {
			t.Errorf("expected deprecation warning on stderr, got: %q", stderr.String())
		}
		if sysCreateFlagValues.outDir != "./host" {
			t.Errorf("outDir = %q, want alias copy %q", sysCreateFlagValues.outDir, "./host")
		}
	})

	t.Run("alias and --out-dir together are a usage error", func(t *testing.T) {
		resetFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--output", "./a", "--out-dir", "./b"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "cannot combine") {
			t.Errorf("unexpected error: %v", err)
		}
		if exitCode(err) != 2 {
			t.Errorf("exitCode = %d, want 2", exitCode(err))
		}
	})
}

func TestSysCreateOutputJSONFormat(t *testing.T) {
	resetFlags := func(t *testing.T) {
		t.Helper()
		sysCreateFlagValues = sysCreateFlags{}
		t.Cleanup(func() { sysCreateFlagValues = sysCreateFlags{} })
	}

	t.Run("--output json maps to outputJSON stdout", func(t *testing.T) {
		resetFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--output", "json"})
		_ = rootCmd.Execute() // scaffold-discovery error expected; flag wiring is what we assert
		if sysCreateFlagValues.outputJSON != "-" {
			t.Errorf("outputJSON = %q, want %q (--output json maps to stdout)", sysCreateFlagValues.outputJSON, "-")
		}
		if sysCreateFlagValues.outDir != "" {
			t.Errorf("outDir = %q, want empty (--output json is not a directory)", sysCreateFlagValues.outDir)
		}
	})

	t.Run("--output json with --output-json path is a usage error", func(t *testing.T) {
		resetFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--output", "json", "--output-json", "result.json"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "cannot combine") {
			t.Errorf("unexpected error: %v", err)
		}
		if exitCode(err) != 2 {
			t.Errorf("exitCode = %d, want 2", exitCode(err))
		}
	})

	t.Run("--output-json - alone warns about deprecation", func(t *testing.T) {
		resetFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--output-json", "-"})
		_ = rootCmd.Execute() // scaffold-discovery error expected
		if !strings.Contains(stderr.String(), "--output-json - is deprecated") {
			t.Errorf("expected deprecation warning for --output-json -, got: %q", stderr.String())
		}
	})
}
