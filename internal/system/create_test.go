package system

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRequiresName(t *testing.T) {
	err := Create(context.Background(), CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "system name is required") {
		t.Fatalf("err = %v", err)
	}
}

// Declining the --force prompt must leave an existing host untouched: the
// hand edits it holds are what the prompt protects.
func TestCreateForceDeclinedLeavesHostUntouched(t *testing.T) {
	out := t.TempDir()
	topics := filepath.Join(out, "Topics.cs")
	if err := os.WriteFile(topics, []byte("// hand-written routing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	asked := ""
	err := Create(context.Background(), CreateOptions{
		Name:      "OrderFlow",
		StartDir:  t.TempDir(),
		OutputDir: out,
		Force:     true,
		ConfirmOverwrite: func(dir string) (bool, error) {
			asked = dir
			return false, nil
		},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("err = %v, want an abort", err)
	}
	if asked != out {
		t.Errorf("asked about %q, want %q", asked, out)
	}
	if got, _ := os.ReadFile(topics); string(got) != "// hand-written routing\n" {
		t.Errorf("Topics.cs = %q, want it unchanged", got)
	}
}

// An empty or missing output directory holds nothing to lose, so --force
// does not ask.
func TestCreateForceDoesNotAskForEmptyDir(t *testing.T) {
	for name, out := range map[string]string{
		"empty":   t.TempDir(),
		"missing": filepath.Join(t.TempDir(), "host"),
	} {
		t.Run(name, func(t *testing.T) {
			_ = Create(context.Background(), CreateOptions{
				Name:      "OrderFlow",
				StartDir:  t.TempDir(), // no scaffolds: fails after the prompt point
				OutputDir: out,
				Force:     true,
				ConfirmOverwrite: func(string) (bool, error) {
					t.Error("ConfirmOverwrite ran for a directory with nothing to lose")
					return false, nil
				},
				Stdout: &bytes.Buffer{},
				Stderr: &bytes.Buffer{},
			})
		})
	}
}
