package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardRejectsMissingDir(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"dashboard", "does-not-exist"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestDashboardRejectsFile(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	file := filepath.Join(tmp, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"dashboard", "afile"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected usage error for non-directory")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("expected usageError (exit 2), got %T: %v", err, err)
	}
}
