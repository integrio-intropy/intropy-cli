package template

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxDependencyDepth bounds transitive dependency chains. Real chains are
// one level deep (component → shared library); the cap only exists to turn
// an authoring cycle that survives the visited-set check into a clear error.
const maxDependencyDepth = 5

// DependencyResult reports what Create did about one declared dependency.
// Part of CreateResult's additive-only JSON contract.
type DependencyResult struct {
	Template  string `json:"template"`
	OutputDir string `json:"outputDir"`
	Action    string `json:"action"` // "created" | "exists"
}

// depContext carries the per-run state shared by every dependency render:
// the extracted templates repo (all templates of the resolved tag are
// already on disk) and a visited set so a diamond — two templates declaring
// the same sibling — renders it once.
type depContext struct {
	repoRoot string
	owner    string
	repo     string
	version  string
	stderr   io.Writer
	visited  map[string]bool
}

// processDependencies renders every spec.dependencies entry of tmpl as a
// sibling of outputDir, recursively, skipping targets that already carry a
// matching scaffold record. It returns the records to embed in the parent's
// scaffold.json and the results for CreateResult.
func processDependencies(tmpl *Template, values map[string]any, outputDir string, dc *depContext, depth int) ([]DependencyRecord, []DependencyResult, error) {
	if len(tmpl.Spec.Dependencies) == 0 {
		return nil, nil, nil
	}
	if depth >= maxDependencyDepth {
		return nil, nil, fmt.Errorf("dependency chain exceeds %d levels — check spec.dependencies for cycles", maxDependencyDepth)
	}

	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, nil, fmt.Errorf("absolute path for %s: %w", outputDir, err)
	}
	parentDir := filepath.Dir(absOut)

	var records []DependencyRecord
	var results []DependencyResult
	for _, dep := range tmpl.Spec.Dependencies {
		name, err := renderExpr(dep.Output, values)
		if err != nil {
			return nil, nil, fmt.Errorf("dependency %s: render output: %w", dep.Template, err)
		}
		if err := validateDependencyOutput(name); err != nil {
			return nil, nil, fmt.Errorf("dependency %s: %w", dep.Template, err)
		}
		depDir := filepath.Join(parentDir, name)
		records = append(records, DependencyRecord{Template: dep.Template, Dir: path.Join("..", name)})

		if dc.visited[depDir] {
			// Another dependency in this same run already claimed the
			// directory; it was rendered (or skipped) there.
			continue
		}
		dc.visited[depDir] = true

		depValues, err := renderDependencyValues(dep, values)
		if err != nil {
			return nil, nil, err
		}

		existing, err := dependencyDirScaffold(depDir, dep.Template)
		if err != nil {
			return nil, nil, err
		}
		if existing != nil {
			warnDependencyDrift(dc.stderr, name, dep, depValues, existing.Values)
			fmt.Fprintf(dc.stderr, "dependency %s already scaffolded from %s — skipped\n", name, dep.Template)
			results = append(results, DependencyResult{Template: dep.Template, OutputDir: depDir, Action: "exists"})
			continue
		}

		created, err := renderDependency(dep, name, depDir, depValues, dc, depth)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, created...)
	}
	return records, results, nil
}

// renderDependency scaffolds one dependency into depDir: resolve its values
// without prompting, render its skeleton, recurse into its own
// dependencies, and write its scaffold record. It returns the result for
// this dependency followed by any transitive ones.
func renderDependency(dep DependencySpec, name, depDir string, sets map[string]any, dc *depContext, depth int) ([]DependencyResult, error) {
	depRoot := filepath.Join(dc.repoRoot, dep.Template)
	depTmpl, err := LoadTemplate(filepath.Join(depRoot, templateManifestName))
	if err != nil {
		return nil, fmt.Errorf("dependency %s: %w", dep.Template, err)
	}

	// No prompter: a dependency render must never block on interactive
	// input, since whether it runs at all depends on the target directory.
	values, err := Resolve(depTmpl, nil, nil, sets, nil)
	if err != nil {
		return nil, fmt.Errorf("dependency %s: %w", dep.Template, err)
	}

	// --force never propagates to dependencies: overwriting a shared
	// project that other components already reference is destructive at a
	// distance. Regenerating one is an explicit `int create <template> --force`.
	if err := renderCreateOutput(filepath.Join(depRoot, templateSkeletonDir), dep.Template, depDir, false, values); err != nil {
		return nil, fmt.Errorf("dependency %s: %w", dep.Template, err)
	}

	subRecords, subResults, err := processDependencies(depTmpl, values, depDir, dc, depth+1)
	if err != nil {
		return nil, err
	}

	if err := WriteScaffold(depDir, Scaffold{
		SchemaVersion: ScaffoldSchemaVersion,
		Template:      dep.Template,
		Owner:         dc.owner,
		Repo:          dc.repo,
		Version:       dc.version,
		Values:        values,
		Role:          roleFromLabels(depTmpl.Metadata.Labels),
		BlockKind:     blockKindFromLabels(depTmpl.Metadata.Labels),
		DataFlow:      dataFlowFromLabels(depTmpl.Metadata.Labels),
		DependsOn:     subRecords,
	}); err != nil {
		return nil, fmt.Errorf("dependency %s: %w", dep.Template, err)
	}

	fmt.Fprintf(dc.stderr, "created dependency %s from template %s\n", name, dep.Template)
	own := DependencyResult{Template: dep.Template, OutputDir: depDir, Action: "created"}
	return append([]DependencyResult{own}, subResults...), nil
}

// renderDependencyValues renders the dependency's values map against the
// parent's resolved values. Every rendered value is a string; Resolve's
// schema-type coercion turns them into booleans/integers where the
// dependency's parameters call for it.
func renderDependencyValues(dep DependencySpec, parentValues map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(dep.Values))
	for k, expr := range dep.Values {
		rendered, err := renderExpr(expr, parentValues)
		if err != nil {
			return nil, fmt.Errorf("dependency %s: render values.%s: %w", dep.Template, k, err)
		}
		out[k] = rendered
	}
	return out, nil
}

// dependencyDirScaffold classifies the dependency target directory. It
// returns the existing scaffold record when the directory is already managed
// by the same template (the skip case), nil when the directory is missing or
// empty (the render case), and an error for anything else — a foreign
// template's project or an unmanaged non-empty directory is never touched.
func dependencyDirScaffold(dir, wantTemplate string) (*Scaffold, error) {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("dependency target %s exists and is not a directory", dir)
	}

	scaffoldPath := filepath.Join(dir, filepath.FromSlash(ScaffoldRelPath))
	if _, err := os.Stat(scaffoldPath); err == nil {
		s, err := LoadScaffold(scaffoldPath)
		if err != nil {
			return nil, err
		}
		if s.Template != wantTemplate {
			return nil, fmt.Errorf("dependency target %s is already scaffolded from template %q, expected %q", dir, s.Template, wantTemplate)
		}
		return s, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, fmt.Errorf("dependency target %s is not empty and has no %s — refusing to overwrite an unmanaged directory", dir, ScaffoldRelPath)
	}
	return nil, nil
}

// warnDependencyDrift compares the values this render would have passed to
// the dependency with what its scaffold record holds, and warns on
// mismatches. Drift is informational: the existing project wins.
func warnDependencyDrift(stderr io.Writer, name string, dep DependencySpec, want map[string]any, recorded map[string]any) {
	var drifted []string
	for k, v := range want {
		rec, ok := recorded[k]
		if !ok {
			continue
		}
		if fmt.Sprint(rec) != fmt.Sprint(v) {
			drifted = append(drifted, fmt.Sprintf("%s (recorded %v, this component derived %v)", k, rec, v))
		}
	}
	if len(drifted) > 0 {
		fmt.Fprintf(stderr, "warning: dependency %s (%s) was scaffolded with different values: %s\n", name, dep.Template, strings.Join(drifted, "; "))
	}
}

// validateDependencyOutput rejects rendered output names that are not a
// plain single path segment, so a dependency can only ever land as a direct
// sibling of the component.
func validateDependencyOutput(name string) error {
	if name == "" {
		return errors.New("output rendered to an empty string")
	}
	if name != filepath.Clean(name) || name == "." || name == ".." ||
		strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return fmt.Errorf("output %q must be a single path segment (no separators, no leading dot)", name)
	}
	return nil
}
