package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/gitops"
)

// What manifest creation decides for one staged file.
const (
	// ActionCreate: the file is not in the repository yet.
	ActionCreate = "create"

	// ActionIdentical: already there, byte for byte. Not written, and not
	// counted as a change — this is what makes a re-run a genuine no-op.
	ActionIdentical = "identical"

	// ActionConflict: already there and different. Manifest creation cannot
	// decide which version should win, so the complete operation is refused.
	ActionConflict = "conflict"
)

// FileAction is one staged file's fate.
type FileAction struct {
	// Rel is relative to the repository root, slash-separated.
	Rel    string `json:"path"`
	Action string `json:"action"`
}

// Writes reports whether the action puts bytes on disk.
func (a FileAction) Writes() bool {
	return a.Action == ActionCreate
}

// stageRels lists every file a render produced, relative to the staging root and
// slash-separated, sorted so a plan reads in tree order.
//
// A symlink is refused rather than listed. A rendered skeleton has no reason to
// produce one, and copying it would mean reading through it to a file the
// template never named.
func stageRels(stagingDir string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(stagingDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stagingDir, p)
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s rendered as %s, and only regular files may be staged", filepath.ToSlash(rel), d.Type())
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read staged manifests: %w", err)
	}
	slices.Sort(rels)
	return rels, nil
}

// classifyStaged decides what to do with each staged file, given what is already
// in the repository.
//
// Rendering straight into the tree is never an option: existing files become
// ordinary GitOps source and may hold image digests or human edits. Staging first
// makes equality and conflicts explicit before the first create.
//
// Every destination is checked before anything is read. A symlink or a path that
// leaves the checkout therefore fails dry-run and diff as well as creation.
func classifyStaged(stagingDir string, dest *destTree, rels []string) ([]FileAction, error) {
	actions := make([]FileAction, 0, len(rels))
	for _, rel := range rels {
		staged := filepath.Join(stagingDir, filepath.FromSlash(rel))
		if err := dest.assertWritable(rel); err != nil {
			return nil, err
		}

		current, err := dest.read(rel)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("read %s: %w", rel, err)
			}
			actions = append(actions, FileAction{Rel: rel, Action: ActionCreate})
			continue
		}
		next, err := os.ReadFile(staged)
		if err != nil {
			return nil, fmt.Errorf("read staged %s: %w", rel, err)
		}

		if string(current) == string(next) {
			actions = append(actions, FileAction{Rel: rel, Action: ActionIdentical})
		} else {
			actions = append(actions, FileAction{Rel: rel, Action: ActionConflict})
		}
	}
	return actions, nil
}

// applyStaged copies the staged files the actions say to write.
//
// Nothing is written until every action has been classified, so a failure part
// way through classification leaves the repository untouched.
//
// The destination is asserted again here rather than trusted from
// classifyStaged: this function is what puts bytes on disk, and it should not be
// safe only by virtue of its caller.
func applyStaged(stagingDir string, dest *destTree, actions []FileAction) ([]string, error) {
	var written []string
	for _, a := range actions {
		if !a.Writes() {
			continue
		}
		if err := dest.assertWritable(a.Rel); err != nil {
			return written, err
		}
		src := filepath.Join(stagingDir, filepath.FromSlash(a.Rel))
		data, err := os.ReadFile(src)
		if err != nil {
			return written, fmt.Errorf("read staged %s: %w", a.Rel, err)
		}
		if err := dest.write(a.Rel, data); err != nil {
			return written, err
		}
		written = append(written, a.Rel)
	}
	return written, nil
}

// OverlaysSegment is the skeleton directory whose contents may legitimately
// differ between environments.
const OverlaysSegment = "overlays/"

// mergeRendered folds one environment's render into the unit's staged tree.
//
// A skeleton is rendered once per environment, because the renderer has no way to
// emit one overlays/<env>/ directory per value — paths are templated, not
// iterated. Everything outside overlays/ must therefore come out identical on
// every pass, and this is where that is enforced: a file that differs between two
// environments means the author used an environment-varying value somewhere it
// does not belong, and silently keeping the last pass would be a tree whose base/
// only matches one environment.
func mergeRendered(from, into, env string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(into, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		next, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if current, err := os.ReadFile(target); err == nil {
			if string(current) == string(next) {
				return nil
			}
			slash := filepath.ToSlash(rel)
			if !strings.HasPrefix(slash, OverlaysSegment) {
				return fmt.Errorf("%s renders differently for environment %q than for an earlier one; a value that varies by environment may only be used under %s",
					slash, env, OverlaysSegment)
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, next, 0o644)
	})
}

// componentRelPath is the repository-relative directory a component's manifests
// live in, slash-separated.
func componentRelPath(domain, system, component string) string {
	return gitops.Coordinate{Domain: domain, System: system, Component: component}.RelPath()
}

// summariseActions counts the actions by kind for the one-line report.
func summariseActions(actions []FileAction) string {
	counts := map[string]int{}
	for _, a := range actions {
		counts[a.Action]++
	}
	var parts []string
	for _, kind := range []string{ActionCreate, ActionConflict, ActionIdentical} {
		if n := counts[kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, kind))
		}
	}
	if len(parts) == 0 {
		return "nothing to do"
	}
	return strings.Join(parts, ", ")
}
