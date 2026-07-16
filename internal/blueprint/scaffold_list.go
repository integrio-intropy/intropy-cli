package blueprint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// listSkipDirs are directory names never worth descending into when scanning
// for projects: VCS metadata, the project record dir itself, dependency
// trees, and build output.
var listSkipDirs = map[string]bool{
	".git":         true,
	".intropy":     true,
	"node_modules": true,
	"bin":          true,
	"dist":         true,
}

// ScaffoldEntry is one project found by ListScaffolds: the directory holding
// the record plus the record itself. The embedded Scaffold flattens into the
// JSON document so each entry is self-describing for machine consumers.
type ScaffoldEntry struct {
	Path string `json:"path"`
	Scaffold
}

// ListScaffolds walks the tree rooted at root and returns an entry for every
// directory that contains .intropy/scaffold.json, in lexical walk order.
// Paths are reported as WalkDir yields them (root-prefixed), so with a
// relative root they stay usable from the caller's working directory. A
// matched directory is not descended into — projects do not nest. Unreadable
// directories and malformed records become warnings rather than failures so
// one bad project cannot hide the rest.
func ListScaffolds(root string) (entries []ScaffoldEntry, warnings []error) {
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, err)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && listSkipDirs[d.Name()] {
			return fs.SkipDir
		}

		record := filepath.Join(path, filepath.FromSlash(ScaffoldRelPath))
		if _, err := os.Stat(record); err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Errorf("stat %s: %w", record, err))
			}
			return nil
		}
		s, err := LoadScaffold(record)
		if err != nil {
			// Still a project root — don't descend looking for more.
			warnings = append(warnings, err)
			return fs.SkipDir
		}
		entries = append(entries, ScaffoldEntry{Path: path, Scaffold: *s})
		return fs.SkipDir
	})
	if walkErr != nil {
		warnings = append(warnings, walkErr)
	}
	return entries, warnings
}
