package deploy

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// localConfigFileName is the state file int local keeps at the workspace root,
// beside the host's .intropy/scaffold.json. It is checked in: the whole team
// runs the same local topology, and CI runs non-interactively.
const localConfigFileName = ".intropy/local.yaml"

func localConfigPath(sourceDir string) string {
	return filepath.Join(sourceDir, filepath.FromSlash(localConfigFileName))
}

// localConfig is .intropy/local.yaml: the team's recorded decision of which
// fixture each connector binds to on the local cluster.
type localConfig struct {
	Bindings map[string]string
}

type localConfigFile struct {
	Connectors map[string]string `yaml:"connectors"`
}

// loadLocalConfig reads the state file. A missing file is an empty config, not
// an error — the first run of a workspace is exactly that case.
func loadLocalConfig(path string) (localConfig, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return localConfig{}, nil
	}
	if err != nil {
		return localConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var f localConfigFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return localConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return localConfig{Bindings: f.Connectors}, nil
}

// saveLocalConfig writes the state file with keys sorted, so a re-record
// produces a minimal, stable diff.
func saveLocalConfig(path string, cfg localConfig) error {
	var sb strings.Builder
	sb.WriteString("# which fixture each connector binds to on the local cluster;\n")
	sb.WriteString("# written by 'intropy int local' and checked in so the team renders the same\n")
	sb.WriteString("connectors:\n")
	for _, name := range slices.Sorted(maps.Keys(cfg.Bindings)) {
		fmt.Fprintf(&sb, "  %s: %s\n", name, cfg.Bindings[name])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
