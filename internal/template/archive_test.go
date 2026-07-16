package template

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTarGz(t *testing.T, prefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		full := name
		if prefix != "" {
			full = prefix + "/" + name
		}
		h := &tar.Header{Name: full, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarGzStripsLeadingDir(t *testing.T) {
	raw := buildTarGz(t, "integrio-intropy-blueprints-abc123", map[string]string{
		"intropy.yaml": "name: x",
		"foo/bar.txt":  "hello",
		"foo/baz.tmpl": "{{.X}}",
	})
	dst := t.TempDir()
	if err := ExtractTarGz(bytes.NewReader(raw), dst); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}
	for _, want := range []string{"intropy.yaml", "foo/bar.txt", "foo/baz.tmpl"} {
		p := filepath.Join(dst, filepath.FromSlash(want))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	raw := buildTarGz(t, "prefix", map[string]string{
		"../escape.txt": "no",
	})
	dst := t.TempDir()
	err := ExtractTarGz(bytes.NewReader(raw), dst)
	if err == nil {
		t.Fatal("expected error for traversal entry")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}
