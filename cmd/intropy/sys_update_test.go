package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSysUpdateOutputValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resetRootIO(t, &stdout, &stderr)
	t.Chdir(t.TempDir())
	t.Cleanup(func() { sysUpdateFlagValues = sysUpdateFlags{} })

	rootCmd.SetArgs([]string{"sys", "update", "--output", "yaml"})
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
}

func TestSysUpdateRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sys", "update"})
	if err != nil {
		t.Fatalf("sys update not registered: %v", err)
	}
	if cmd != sysUpdateCmd {
		t.Errorf("sys update resolves to %q, want the sys update command", cmd.CommandPath())
	}
	for _, flag := range []string{"output", "dry-run", "force", "template-version", "template-repo"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("flag --%s not registered", flag)
		}
	}
}
