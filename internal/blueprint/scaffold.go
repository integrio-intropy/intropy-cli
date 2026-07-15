package blueprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// ScaffoldSchemaVersion is the newest scaffold.json schema this CLI
	// understands. Bump only for incompatible changes; additive fields do
	// not require a bump.
	ScaffoldSchemaVersion = 1

	// ScaffoldRelPath is where the scaffold record lives inside a project.
	ScaffoldRelPath = ".intropy/scaffold.json"
)

var ErrScaffoldNotFound = errors.New("no " + ScaffoldRelPath + " found in current directory or any parent")

// Scaffold is the committed record of what `int create` rendered. Unlike
// CreateResult it carries no outputDir — the file's location is the project
// root — and it is written into the project so later commands (e.g.
// `manifests create`) can re-fetch the exact blueprint version.
type Scaffold struct {
	SchemaVersion int            `json:"schemaVersion"`
	Blueprint     string         `json:"blueprint"` // directory name in the blueprints repo
	Owner         string         `json:"owner"`
	Repo          string         `json:"repo"`
	Version       string         `json:"version"`
	Values        map[string]any `json:"values"`
}

// WriteScaffold writes the scaffold record to <projectRoot>/.intropy/scaffold.json.
func WriteScaffold(projectRoot string, s Scaffold) error {
	path := filepath.Join(projectRoot, filepath.FromSlash(ScaffoldRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write scaffold record: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("write scaffold record: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write scaffold record: %w", err)
	}
	return nil
}

// LoadScaffold reads and parses a scaffold.json file.
func LoadScaffold(path string) (*Scaffold, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scaffold record %s: %w", path, err)
	}
	var s Scaffold
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse scaffold record %s: %w", path, err)
	}
	return &s, nil
}

// FindScaffold walks up from startDir looking for .intropy/scaffold.json.
// Returns the parsed record and the project root that contains it, or
// ErrScaffoldNotFound if the filesystem root is reached first.
func FindScaffold(startDir string) (*Scaffold, string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return nil, "", fmt.Errorf("absolute path: %w", err)
	}

	for {
		candidate := filepath.Join(abs, filepath.FromSlash(ScaffoldRelPath))
		if _, err := os.Stat(candidate); err == nil {
			s, err := LoadScaffold(candidate)
			if err != nil {
				return nil, "", err
			}
			return s, abs, nil
		} else if !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("stat %s: %w", candidate, err)
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			return nil, "", ErrScaffoldNotFound
		}
		abs = parent
	}
}
