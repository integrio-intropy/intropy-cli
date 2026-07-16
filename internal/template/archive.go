package template

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractTarGz extracts a gzipped tar stream into destDir. It strips the
// single top-level directory wrapper that GitHub adds to source tarballs.
func ExtractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		rel := stripLeadingDir(hdr.Name)
		if rel == "" || rel == "." {
			continue
		}
		if err := validateRelPath(rel); err != nil {
			return err
		}
		target := filepath.Join(destDir, filepath.FromSlash(rel))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := clampMode(os.FileMode(hdr.Mode))
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// Skip symlinks, fifos, devices, etc.
		}
	}
}

func stripLeadingDir(name string) string {
	name = filepath.ToSlash(name)
	name = strings.TrimPrefix(name, "./")
	i := strings.Index(name, "/")
	if i < 0 {
		return ""
	}
	return name[i+1:]
}

func validateRelPath(rel string) error {
	if filepath.IsAbs(rel) {
		return fmt.Errorf("archive entry has absolute path: %q", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("archive entry escapes destination: %q", rel)
	}
	return nil
}

func clampMode(m os.FileMode) os.FileMode {
	if m.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}
