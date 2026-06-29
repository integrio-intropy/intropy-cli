package blueprint

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

const tmplSuffix = ".tmpl"

// Render walks srcDir and writes results into destDir. Both path segments and
// file contents flow through text/template with sprig helpers. A .tmpl suffix
// on a file's basename triggers content rendering and is stripped from the
// destination path. Examples:
//   - skeleton/README.md.tmpl          → README.md   (contents rendered)
//   - skeleton/{{.Name}}/svc.go        → <Name>/svc.go (path rendered, contents copied)
//   - skeleton/{{.Name}}.http.tmpl     → <Name>.http (path and contents rendered)
//
// srcDir is the blueprint's skeleton/ directory; the manifest lives outside
// this tree.
func Render(srcDir, destDir string, values map[string]any) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		renderedRel, err := renderPath(rel, values)
		if err != nil {
			return err
		}
		if renderedRel == "" {
			return fmt.Errorf("path %q rendered to empty string", rel)
		}
		target := filepath.Join(destDir, strings.TrimSuffix(renderedRel, tmplSuffix))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if strings.HasSuffix(renderedRel, tmplSuffix) {
			return renderTemplate(path, target, mode, values)
		}
		return copyFile(path, target, mode)
	})
}

func renderPath(rel string, values map[string]any) (string, error) {
	if !strings.Contains(rel, "{{") {
		return rel, nil
	}
	t, err := template.New("path").
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").
		Parse(rel)
	if err != nil {
		return "", fmt.Errorf("parse path %q: %w", rel, err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, values); err != nil {
		return "", fmt.Errorf("render path %q: %w", rel, err)
	}
	return buf.String(), nil
}

func renderTemplate(src, dst string, mode os.FileMode, values map[string]any) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	t, err := template.New(filepath.Base(src)).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").
		Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parse %s: %w", src, err)
	}
	return writeAtomically(dst, mode, func(w io.Writer) error {
		if err := t.Execute(w, values); err != nil {
			return fmt.Errorf("render %s: %w", src, err)
		}
		return nil
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	return writeAtomically(dst, mode, func(w io.Writer) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
}

func writeAtomically(dst string, mode os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	committed = true
	return nil
}
