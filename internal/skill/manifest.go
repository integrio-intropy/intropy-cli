package skill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Manifest is the root of skills.json: the skills a project has installed
// and the collections it resolves bare names against.
type Manifest struct {
	Collections []ManifestCollection `json:"collections,omitempty"`
	Skills      []ManifestEntry      `json:"skills"`
}

// ManifestEntry is one installed skill. Source is the registry/repository
// pair; Version is the OCI tag. A bare Source with no Version resolves to
// the latest tag.
//
// §6.3 of the Agent Skills OCI spec.
type ManifestEntry struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
}

// ManifestCollection is one registered collection: a local alias and the
// OCI ref of its published index.
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

// Validate rejects a manifest with a nameless or sourceless entry, or a
// duplicate skill name. Called on both load and save so a hand-edited file
// fails at the point it is read, not at the point it is acted on.
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
