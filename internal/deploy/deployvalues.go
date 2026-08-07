package deploy

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// deployValuesFileName is the state file deploy init keeps at the workspace
// root, beside the host's .intropy/scaffold.json. It is checked in: the whole
// team scaffolds the same connectors, and CI runs non-interactively.
const deployValuesFileName = ".intropy/deploy-values.yaml"

// legacyLocalConfigFileName is the state file int local kept, absorbed into
// deploy-values.yaml: its bindings become the "local" environment's entries.
const legacyLocalConfigFileName = ".intropy/local.yaml"

func deployValuesPath(sourceDir string) string {
	return filepath.Join(sourceDir, filepath.FromSlash(deployValuesFileName))
}

// deployValues is .intropy/deploy-values.yaml: the team's recorded decision of
// which Dapr binding each connector deploys as, per environment. "local" is
// one environment among them; its values name fixtures rather than binding
// types, but the mechanism is the same.
type deployValues struct {
	// Connectors maps connector name to environment to binding.
	Connectors map[string]map[string]string
}

type deployValuesFile struct {
	Connectors map[string]map[string]string `yaml:"connectors"`
}

// loadDeployValues reads the state file. A missing file is an empty config,
// not an error — the first run of a workspace is exactly that case.
func loadDeployValues(path string) (deployValues, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return deployValues{}, nil
	}
	if err != nil {
		return deployValues{}, fmt.Errorf("read %s: %w", path, err)
	}
	var f deployValuesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return deployValues{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return deployValues{Connectors: f.Connectors}, nil
}

// saveDeployValues writes the state file with keys sorted at both levels, so
// a re-record produces a minimal, stable diff.
func saveDeployValues(path string, vals deployValues) error {
	var sb strings.Builder
	sb.WriteString("# which Dapr binding each connector deploys as, per environment;\n")
	sb.WriteString("# written by 'intropy deploy init' and checked in so the team scaffolds the same\n")
	sb.WriteString("connectors:\n")
	for _, name := range slices.Sorted(maps.Keys(vals.Connectors)) {
		fmt.Fprintf(&sb, "  %s:\n", name)
		for _, env := range slices.Sorted(maps.Keys(vals.Connectors[name])) {
			fmt.Fprintf(&sb, "    %s: %s\n", env, vals.Connectors[name][env])
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// migrateLegacyLocalConfig folds an old .intropy/local.yaml into the values
// file as the local environment's bindings, and removes the legacy file.
// Both files absent is the first-run case and not an error; both present is
// left alone — merging two sources silently would guess at which one is
// newer, so the resolver's catalog validation surfaces any conflict instead.
func migrateLegacyLocalConfig(sourceDir string, stderr io.Writer) error {
	legacy := filepath.Join(sourceDir, filepath.FromSlash(legacyLocalConfigFileName))
	data, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", legacy, err)
	}
	current := deployValuesPath(sourceDir)
	if _, err := os.Stat(current); err == nil {
		fmt.Fprintf(stderr, "note: both %s and %s exist; leaving the migration alone\n", legacyLocalConfigFileName, deployValuesFileName)
		return nil
	}

	var legacyFile struct {
		Connectors map[string]string `yaml:"connectors"`
	}
	if err := yaml.Unmarshal(data, &legacyFile); err != nil {
		return fmt.Errorf("parse %s: %w", legacy, err)
	}
	vals := deployValues{Connectors: map[string]map[string]string{}}
	for name, binding := range legacyFile.Connectors {
		vals.Connectors[name] = map[string]string{localEnv: binding}
	}
	if err := saveDeployValues(current, vals); err != nil {
		return err
	}
	if err := os.Remove(legacy); err != nil {
		return fmt.Errorf("remove migrated %s: %w", legacy, err)
	}
	fmt.Fprintf(stderr, "migrated %s into %s\n", legacyLocalConfigFileName, deployValuesFileName)
	return nil
}
