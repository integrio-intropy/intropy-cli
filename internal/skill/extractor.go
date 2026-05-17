package skill

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Extractor unpacks a skill's tar+gzip content layer to one or more destination
// directories. Implementations must:
//   - reject path traversal via ".." in archive entries
//   - reject symlinks
//   - preserve POSIX permissions on regular files
//   - create parent directories as needed
type Extractor interface {
	Extract(ctx context.Context, layer io.Reader, dests []string) error
}

type tarGzExtractor struct{}

func NewTarGzExtractor() Extractor { return tarGzExtractor{} }

func (tarGzExtractor) Extract(ctx context.Context, layer io.Reader, dests []string) error {
	if len(dests) == 0 {
		return errors.New("no destination path")
	}

	primary := dests[0]
	if err := extractTo(layer, primary); err != nil {
		return fmt.Errorf("extract to %s: %w", primary, err)
	}

	for _, dest := range dests[1:] {
		if err := copyTree(primary, dest); err != nil {
			return fmt.Errorf("fan out to %s: %w", dest, err)
		}
	}

	return nil
}

func extractTo(layer io.Reader, dest string) error {
	// Wipe-and-replace
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clean %s: %w", dest, err)
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	gz, err := gzip.NewReader(layer)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	destClean := filepath.Clean(dest) + string(os.PathSeparator)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Strip the spec-mandated leading "<skill-name>/" component.
		// The tar is rooted at <skill-name>/, but we install AT
		// .agents/skills/<skill-name>/ — i.e. the destination already
		// includes the skill name, so we drop it from each entry.
		archPath := stripFirstPathComponent(hdr.Name)
		if archPath == "" {
			// The root <skill-name>/ entry itself; nothing to write.
			continue
		}

		target := filepath.Join(dest, archPath)

		// Path traversal defense: after Join, target must still be inside dest.
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destClean) {
			return fmt.Errorf("illegal path in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fs.FileMode(hdr.Mode).Perm()); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := writeFile(tr, target, fs.FileMode(hdr.Mode).Perm()); err != nil {
				return fmt.Errorf("write file %s: %w", target, err)
			}
		case tar.TypeSymlink:
			return fmt.Errorf("symlinks not supported in skill archives: %s", hdr.Name)
		default:
			return fmt.Errorf("unsupported tar entry type %v: %s", hdr.Typeflag, hdr.Name)
		}
	}
}

func stripFirstPathComponent(p string) string {
	p = strings.TrimPrefix(p, "./")
	if _, after, ok := strings.Cut(p, "/"); ok {
		return after
	}
	return ""
}

func writeFile(r io.Reader, target string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func copyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file in tree: %s", path)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}

	return out.Close()
}
