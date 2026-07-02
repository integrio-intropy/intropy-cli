package skill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type Manifest struct {
	Collections []ManifestCollection `json:"collections,omitempty"`
	Skills      []ManifestEntry      `json:"skills"`
}

// §6.3
type ManifestEntry struct {
	Name                string   `json:"name"`
	Source              string   `json:"source"`
	Version             string   `json:"version,omitempty"`
	AdditionalBasePaths []string `json:"additionalBasePaths,omitempty"`
}

type ManifestCollection struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// LoadManifest reads and parses skills.json from path.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}

	return &m, nil
}

// SaveManifest writes the manifest to path with indented JSON.
// The output is human-edited file material, so prettiness matters.
func SaveManifest(path string, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	// Trailing newline; many tools add one and not having it shows up as
	// diff noise in PRs.
	data = append(data, '\n')

	return os.WriteFile(path, data, 0644)
}

func (m *Manifest) Validate() error {
	seen := map[string]struct{}{}
	for i, e := range m.Skills {
		if e.Name == "" {
			return fmt.Errorf("skill[%d]: name is required.", i)
		}
		if e.Source == "" {
			return fmt.Errorf("skill[%d] %q: source is required.", i, e.Name)
		}
		if _, dup := seen[e.Name]; dup {
			return fmt.Errorf("duplicate skill name: %q", e.Name)
		}
		seen[e.Name] = struct{}{}
	}

	return nil
}
