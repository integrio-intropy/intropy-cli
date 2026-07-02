package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Pack tars a skill directory into an Artifact. The skill's SKILL.md must have
// YAML frontmatter; its parsed contents become the Config. Symlinks, "..", and
// non-regular files are rejected (mirrors the security stance of Extractor).
func Pack(skillDir string) (Artifact, error) {
	info, err := os.Stat(skillDir)
	if err != nil {
		return Artifact{}, fmt.Errorf("stat %s: %w", skillDir, err)
	}
	if !info.IsDir() {
		return Artifact{}, fmt.Errorf("%s is not a directory", skillDir)
	}

	skillMD, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return Artifact{}, fmt.Errorf("read SKILL.md: %w", err)
	}

	cfg, err := parseFrontMatter(skillMD)
	if err != nil {
		return Artifact{}, fmt.Errorf("parse SKILL.md frontmatter: %w", err)
	}

	type entry struct {
		absPath  string
		archPath string
		info     os.FileInfo
	}

	skillRoot := cfg.Name
	var entries []entry

	err = filepath.WalkDir(skillDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == skillDir {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Refuse symlinks: their target may sit outside the source directory,
		// which would make the archive non-self-contained and let an extractor
		// that follows the link write outside the destination.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not supported in skill archives: %s", path)
		}

		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		archPath := filepath.ToSlash(filepath.Join(skillRoot, rel))

		entries = append(entries, entry{absPath: path, archPath: archPath, info: info})
		return nil
	})
	if err != nil {
		return Artifact{}, fmt.Errorf("walk %s: %w", skillDir, err)
	}

	// Deterministic order for stable digests.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].archPath < entries[j].archPath
	})

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	epoch := time.Unix(0, 0)

	if err := tw.WriteHeader(&tar.Header{
		Name:     skillRoot + "/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
		ModTime:  epoch,
	}); err != nil {
		return Artifact{}, fmt.Errorf("write root header: %w", err)
	}

	for _, e := range entries {
		hdr := &tar.Header{
			Name:    e.archPath,
			Mode:    int64(e.info.Mode().Perm()),
			ModTime: epoch,
			Size:    e.info.Size(),
		}

		switch {
		case e.info.IsDir():
			hdr.Typeflag = tar.TypeDir
			hdr.Name += "/"
			hdr.Size = 0
		case e.info.Mode().IsRegular():
			hdr.Typeflag = tar.TypeReg
		default:
			return Artifact{}, fmt.Errorf("unsupported file type: %s", e.absPath)
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return Artifact{}, fmt.Errorf("write header for %s: %w", e.archPath, err)
		}

		if hdr.Typeflag == tar.TypeReg {
			f, err := os.Open(e.absPath)
			if err != nil {
				return Artifact{}, fmt.Errorf("open %s: %w", e.absPath, err)
			}
			if _, err := io.Copy(tw, f); err != nil {
				f.Close()
				return Artifact{}, fmt.Errorf("copy %s: %w", e.absPath, err)
			}
			f.Close()
		}
	}

	if err := tw.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close gzip: %w", err)
	}

	return Artifact{
		Config:  cfg,
		Content: io.NopCloser(bytes.NewReader(buf.Bytes())),
		Tag:     "",
		Digest:  "",
	}, nil
}
