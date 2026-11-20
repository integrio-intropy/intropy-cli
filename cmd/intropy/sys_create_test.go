package main

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestSysCreateOutputValidation(t *testing.T) {
	resetFlags := func(t *testing.T) {
		t.Helper()
		sysCreateFlagValues = sysCreateFlags{}
		t.Cleanup(func() { sysCreateFlagValues = sysCreateFlags{} })
	}

	t.Run("non-json --output is a usage error", func(t *testing.T) {
		resetFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--output", "./host"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid output format") {
			t.Errorf("unexpected error: %v", err)
		}
		if exitCode(err) != 2 {
			t.Errorf("exitCode = %d, want 2", exitCode(err))
		}
	})
}

// Without a terminal --force cannot ask, so it warns on stderr and proceeds;
// scripts that pass --force keep working.
func TestSysCreateForceWarnsWithoutTerminal(t *testing.T) {
	sysCreateFlagValues = sysCreateFlags{}
	t.Cleanup(func() { sysCreateFlagValues = sysCreateFlags{} })
	var stdout, stderr bytes.Buffer
	resetRootIO(t, &stdout, &stderr)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "order-flow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "order-flow", "Topics.cs"), []byte("// edits\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"sys", "create", "-n", "OrderFlow", "--force"})
	_ = rootCmd.Execute() // no scaffolds in the workspace: fails after the warning

	for _, want := range []string{"warning: --force overwrites", "order-flow", "intropy sys update"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr should mention %q: %q", want, stderr.String())
		}
	}
}
