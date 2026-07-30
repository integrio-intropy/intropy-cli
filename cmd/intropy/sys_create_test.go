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


