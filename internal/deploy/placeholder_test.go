package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScanFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanPlaceholdersReportsFileAndLine(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "host/base/secrets/secrets.yaml", `apiVersion: v1
kind: Secret
stringData:
  connection: REPLACE-ME-RABBITMQ-CONNECTION-STRING
  host: REPLACE-ME-SFTP-HOST
`)

	found, err := scanPlaceholders(root, []string{"host/base/secrets/secrets.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %+v, want 2", found)
	}
	if found[0].Line != 4 || found[0].Token != "REPLACE-ME-RABBITMQ-CONNECTION-STRING" {
		t.Errorf("found[0] = %+v", found[0])
	}
	if found[1].Line != 5 || found[1].Token != "REPLACE-ME-SFTP-HOST" {
		t.Errorf("found[1] = %+v", found[1])
	}
	if found[0].File != "host/base/secrets/secrets.yaml" {
		t.Errorf("File = %q, want a slash-separated relative path", found[0].File)
	}
}

// Pinning is intropy deploy's job. Reporting the tag here would invite someone
// to hand-edit it, which is exactly the discipline this package enforces.
func TestScanPlaceholdersIgnoresUnpinnedImageTag(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "base/deployment.yaml", `spec:
  containers:
    - image: harbor.intropy.io/fluxia/order-extract:unpinned
`)

	found, err := scanPlaceholders(root, []string{"base/deployment.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("found = %+v, want none", found)
	}
}

func TestScanPlaceholdersFindsSeveralOnOneLine(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "a.yaml", "url: sftp://REPLACE-ME-SFTP-USER@REPLACE-ME-SFTP-HOST/in\n")

	found, err := scanPlaceholders(root, []string{"a.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %+v, want 2", found)
	}
	// Sorted by token within a line, so output is stable.
	if found[0].Token != "REPLACE-ME-SFTP-HOST" || found[1].Token != "REPLACE-ME-SFTP-USER" {
		t.Errorf("tokens = %q, %q", found[0].Token, found[1].Token)
	}
}

// The README the template renders explains each placeholder, so it must be
// scanned too rather than only the YAML.
func TestScanPlaceholdersCoversMarkdown(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "README.md", "Set `REPLACE-ME-KEYVAULT-URL` to the vault endpoint.\n")

	found, err := scanPlaceholders(root, []string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Token != "REPLACE-ME-KEYVAULT-URL" {
		t.Fatalf("found = %+v", found)
	}
}

// Trailing prose punctuation must not become part of the token, or the reported
// value would not match what is in the file.
func TestScanPlaceholdersStopsAtPunctuation(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "a.md", "set REPLACE-ME-SFTP-HOST, then restart\n")

	found, err := scanPlaceholders(root, []string{"a.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Token != "REPLACE-ME-SFTP-HOST" {
		t.Fatalf("found = %+v", found)
	}
}

func TestScanPlaceholdersSkipsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "blob.bin")
	if err := os.WriteFile(path, []byte("REPLACE-ME-X\x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := scanPlaceholders(root, []string{"blob.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("found = %+v, want none in a binary file", found)
	}
}

// A re-run scans the committed tree, where a file classified skip-exists is
// present but one that was never written is not.
func TestScanPlaceholdersSkipsMissingFiles(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "present.yaml", "x: REPLACE-ME-A\n")

	found, err := scanPlaceholders(root, []string{"absent.yaml", "present.yaml"})
	if err != nil {
		t.Fatalf("a missing path must not be an error: %v", err)
	}
	if len(found) != 1 || found[0].File != "present.yaml" {
		t.Fatalf("found = %+v", found)
	}
}

func TestScanPlaceholdersIsSortedAcrossFiles(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "z.yaml", "a: REPLACE-ME-Z\n")
	writeScanFile(t, root, "a.yaml", "a: REPLACE-ME-A\n")

	found, err := scanPlaceholders(root, []string{"z.yaml", "a.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].File != "a.yaml" || found[1].File != "z.yaml" {
		t.Errorf("found = %+v, want them sorted by file", found)
	}
}

func TestReportPlaceholdersSaysImagesAreNotIncluded(t *testing.T) {
	var stdout bytes.Buffer
	out := output{Stdout: &stdout, Format: OutputPlain}

	reportPlaceholders(out, []Placeholder{
		{File: "host/base/secrets/secrets.yaml", Line: 4, Token: "REPLACE-ME-RABBITMQ-CONNECTION-STRING"},
	})

	got := stdout.String()
	for _, want := range []string{"1 placeholder", "1 file", "secrets.yaml:4", "intropy deploy pins digests"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestReportPlaceholdersOnNone(t *testing.T) {
	var stdout bytes.Buffer
	reportPlaceholders(output{Stdout: &stdout, Format: OutputPlain}, nil)
	if !strings.Contains(stdout.String(), "no placeholders") {
		t.Errorf("output = %q", stdout.String())
	}
}
