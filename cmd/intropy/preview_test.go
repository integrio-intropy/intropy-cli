package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMarkPreviewAnnotatesHelp(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "frob",
		Short: "Frob the thing",
		Long:  "Frob the thing in detail.",
	}
	markPreview(cmd)

	if cmd.Annotations[previewAnnotation] != "true" {
		t.Error("markPreview must set the preview annotation")
	}
	if !strings.HasSuffix(cmd.Short, " (preview)") {
		t.Errorf("Short = %q, want a ' (preview)' suffix", cmd.Short)
	}
	if !strings.Contains(cmd.Long, "may change or be removed") {
		t.Errorf("Long = %q, want the preview stability note", cmd.Long)
	}

	bare := &cobra.Command{Use: "bare", Short: "Do something"}
	markPreview(bare)
	if strings.Contains(bare.Long, "\n\n") {
		t.Errorf("Long with no prior text must not gain a leading blank line, got %q", bare.Long)
	}
}

func TestIsPreviewWalksUp(t *testing.T) {
	group := &cobra.Command{Use: "grp"}
	markPreview(group)
	child := &cobra.Command{Use: "sub"}
	group.AddCommand(child)

	if !isPreview(child) {
		t.Error("subcommand of a preview group must count as preview")
	}
	if isPreview(&cobra.Command{Use: "plain"}) {
		t.Error("unmarked command must not count as preview")
	}
}

func TestPreviewWarningGoesToStderr(t *testing.T) {
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	resetChangeDirFlag(t)

	rootCmd.SetArgs([]string{"dashboard", "does-not-exist"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for missing directory")
	}

	want := "warning: 'intropy dashboard' is a preview command and may change or be removed\n"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want it empty; the warning belongs on stderr", stdout.String())
	}
}

func TestNonPreviewCommandStaysQuiet(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	resetChangeDirFlag(t)

	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.Contains(stderr.String(), "preview") {
		t.Errorf("stderr = %q, want no preview warning for a stable command", stderr.String())
	}
}
