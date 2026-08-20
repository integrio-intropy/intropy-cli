package template

import (
	"bytes"
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
// srcDir is the template's skeleton/ directory; the manifest lives outside
// this tree.
func Render(srcDir, destDir string, values map[string]any) error {
	return RenderFiltered(srcDir, destDir, values, nil)
}

// RenderFiltered is Render restricted to the paths the template's spec.files
// rules include. Rules are matched against the source path, and a rule that
// excludes a directory prunes it before anything inside is parsed — so a
// skeleton may carry files that are not even valid templates for the values in
// play. Passing no rules is exactly Render.
func RenderFiltered(srcDir, destDir string, values map[string]any, rules []FileRule) error {
	filter := newSkeletonFilter(rules, values)
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
		include, err := filter.include(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if !include {
			if d.IsDir() {
				return fs.SkipDir
			}
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

// FileOutcomeKind is what an update render decided about one destination
// file.
type FileOutcomeKind string

const (
	OutcomeCreated   FileOutcomeKind = "created"
	OutcomeUnchanged FileOutcomeKind = "unchanged"
	OutcomeUpdated   FileOutcomeKind = "updated"
	OutcomeConflict  FileOutcomeKind = "conflict"
)

// FileOutcome pairs a destination path (relative to the output directory,
// slash-separated) with the decision the update render made for it.
type FileOutcome struct {
	Path    string          `json:"path"`
	Outcome FileOutcomeKind `json:"outcome"`
}

// RenderUpdateOptions controls RenderUpdate.
type RenderUpdateOptions struct {
	// Force turns a conflict into an update: the differing file is
	// overwritten.
	Force bool
	// DryRun computes outcomes without persisting anything.
	DryRun bool
	// Baseline, when set, is the value set the destination was last
	// rendered from. A file matching the baseline render differs from the
	// merged render only by the update itself, so it is updated; a file
	// matching neither is a genuine divergence (a hand edit or drift) and
	// conflicts. Without a baseline every differing file conflicts.
	Baseline map[string]any
}

// RenderUpdate is RenderFiltered with per-file outcomes instead of
// unconditional overwrite: rendering produces bytes first, then each file is
// classified against the destination — absent is created, identical to the
// merged render is unchanged, identical to the baseline render is updated,
// and anything else is a conflict (or, with Force, an update). Nothing is
// written on dry-run, and nothing is written past the first conflict without
// Force, so a caller can refuse a divergent tree without leaving a partial
// render behind. The destination is not subjected to the create flow's
// non-empty refusal: updating means rendering into a directory that already
// holds a project.
func RenderUpdate(srcDir, destDir string, values map[string]any, rules []FileRule, opts RenderUpdateOptions) ([]FileOutcome, error) {
	filter := newSkeletonFilter(rules, values)
	var outcomes []FileOutcome
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
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
		include, err := filter.include(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if !include {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		renderedRel, err := renderPath(rel, values)
		if err != nil {
			return err
		}
		if renderedRel == "" {
			return fmt.Errorf("path %q rendered to empty string", rel)
		}
		outRel := filepath.ToSlash(strings.TrimSuffix(renderedRel, tmplSuffix))
		target := filepath.Join(destDir, filepath.FromSlash(outRel))
		if d.IsDir() {
			if !opts.DryRun {
				return os.MkdirAll(target, 0o755)
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		merged, err := renderFileBytes(path, renderedRel, values)
		if err != nil {
			return err
		}
		// A baseline render that fails (an older record missing a value the
		// template needs) is unknown, not an error: the strict comparison
		// below then decides, conservatively.
		var baseline []byte
		baselineKnown := false
		if opts.Baseline != nil {
			if b, berr := renderFileBytes(path, renderedRel, opts.Baseline); berr == nil {
				baseline = b
				baselineKnown = true
			}
		}
		existing, err := os.ReadFile(target)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var outcome FileOutcomeKind
		switch {
		case err != nil:
			outcome = OutcomeCreated
		case bytes.Equal(existing, merged):
			outcome = OutcomeUnchanged
		case baselineKnown && bytes.Equal(existing, baseline):
			outcome = OutcomeUpdated
		case !opts.Force:
			outcome = OutcomeConflict
		default:
			outcome = OutcomeUpdated
		}
		outcomes = append(outcomes, FileOutcome{Path: outRel, Outcome: outcome})
		if outcome == OutcomeUnchanged || outcome == OutcomeConflict || opts.DryRun {
			return nil
		}
		return writeAtomically(target, info.Mode().Perm(), func(w io.Writer) error {
			_, err := w.Write(merged)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return outcomes, nil
}

// renderFileBytes produces the bytes one skeleton file lands as: rendered
// for a .tmpl source, copied verbatim otherwise.
func renderFileBytes(src, renderedRel string, values map[string]any) ([]byte, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(renderedRel, tmplSuffix) {
		return raw, nil
	}
	t, err := template.New(filepath.Base(src)).
		Funcs(sprig.TxtFuncMap()).
		Option("missingkey=error").
		Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", src, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, values); err != nil {
		return nil, fmt.Errorf("render %s: %w", src, err)
	}
	return buf.Bytes(), nil
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
