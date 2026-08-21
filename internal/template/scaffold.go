package template

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

	// TemplateRoleLabel names the manifest label that declares what role a
	// template's output plays in a system. Its value is copied into the
	// scaffold record so later commands can tell support projects apart
	// from system blocks.
	TemplateRoleLabel = "intropy.dev/template-role"

	// RoleSharedLibrary marks a scaffolded project that exists to be
	// referenced by sibling components (e.g. shared models). System
	// assembly must not treat it as a block.
	RoleSharedLibrary = "shared-library"

	// RoleSystemHost marks the scaffolded host project of an integration
	// system. System assembly must not treat it as a block.
	RoleSystemHost = "system-host"

	// TemplateBlockKindLabel names the manifest label that declares which
	// Intropy block a template scaffolds (e.g. "extractor"). Its value is
	// copied into the scaffold record so `sys create` can assemble the
	// system declaration from what each scaffold recorded.
	TemplateBlockKindLabel = "intropy.dev/block-kind"

	// TemplateDataFlowLabel names the manifest label that declares the
	// block's data flow direction relative to the system ("in", "out",
	// or "both"). Recorded alongside the block kind.
	TemplateDataFlowLabel = "intropy.dev/data-flow"

	// The BlockKind constants name the block kinds with a parse entry in
	// internal/system's blockParsers registry — the set `sys create`
	// assembles. Records carrying any other kind are skipped with a
	// warning.
	BlockKindExtractor     = "extractor"
	BlockKindLoader        = "loader"
	BlockKindTransactional = "transactional-integration"
)

var ErrScaffoldNotFound = errors.New("no " + ScaffoldRelPath + " found in current directory or any parent")

// Scaffold is the committed record of what `int create` rendered. Unlike
// CreateResult it carries no outputDir — the file's location is the project
// root — and it is written into the project so later commands (e.g. `sys
// create`) can re-fetch the exact template version.
type Scaffold struct {
	SchemaVersion int            `json:"schemaVersion"`
	Template      string         `json:"template"` // directory name in the template library repo
	Owner         string         `json:"owner"`
	Repo          string         `json:"repo"`
	Version       string         `json:"version"`
	Values        map[string]any `json:"values"`

	// Role is the value of the template's intropy.dev/template-role label,
	// if any (e.g. "shared-library").
	Role string `json:"role,omitempty"`

	// BlockKind is the value of the template's intropy.dev/block-kind
	// label, if any (e.g. "extractor").
	BlockKind string `json:"blockKind,omitempty"`

	// DataFlow is the value of the template's intropy.dev/data-flow label,
	// if any ("in" or "out").
	DataFlow string `json:"dataFlow,omitempty"`

	// DependsOn lists the sibling projects this project's template declared
	// under spec.dependencies, whether the render created them or they
	// already existed.
	DependsOn []DependencyRecord `json:"dependsOn,omitempty"`
}

// DependencyRecord points at a sibling project a component depends on. Dir
// is slash-separated and relative to the component root (e.g. "../Acme.Models").
type DependencyRecord struct {
	Template string `json:"template"`
	Dir      string `json:"dir"`
}

func roleFromLabels(labels map[string]string) string {
	return labels[TemplateRoleLabel]
}

func blockKindFromLabels(labels map[string]string) string {
	return labels[TemplateBlockKindLabel]
}

func dataFlowFromLabels(labels map[string]string) string {
	return labels[TemplateDataFlowLabel]
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
