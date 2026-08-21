package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetChangeDirFlag(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		changeDirFlag = ""
	})
}

// resetRootIO points the root command at the given buffers and restores
// default output, error, and args on cleanup. Shared by every command test.
func resetRootIO(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRootChangeDirectory(t *testing.T) {
	projectDir := t.TempDir()

	t.Chdir(t.TempDir())
	resetChangeDirFlag(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"-C", projectDir, "version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout.String(), version) {
		t.Errorf("expected version output, got:\n%s", stdout.String())
	}
}

func TestRootChangeDirectoryNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	resetChangeDirFlag(t)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)

	rootCmd.SetArgs([]string{"-C", filepath.Join(t.TempDir(), "nonexistent"), "version"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	if !strings.Contains(err.Error(), "cannot change to directory") {
		t.Errorf("expected 'cannot change to directory' error, got %v", err)
	}
}
