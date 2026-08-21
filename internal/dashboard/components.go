package dashboard

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DaprComponent is a parsed Dapr component definition surfaced to the flow
// canvas. Category is derived from Type so the frontend can place the node
// without re-parsing: "pubsub" and "state" render inside the system boundary,
// "binding" renders outside it — as a source when Direction is "input", a
// sink otherwise.
type DaprComponent struct {
	Name     string `json:"name"`     // metadata.name, e.g. "pubsub"
	Type     string `json:"type"`     // spec.type, e.g. "pubsub.in-memory"
	Category string `json:"category"` // "pubsub" | "state" | "binding" | "other"
	// Direction is the component's declared direction metadata, normalized
	// to "input", "output" or "input,output". Empty when the component does
	// not declare one (or declares something unrecognized) — consumers must
	// treat that as unknown rather than infer a direction.
	Direction string `json:"direction,omitempty"`
	File      string `json:"file"` // source filename, for reference
}

// componentYAML is the minimal shape read from a Dapr component file; every
// other field is ignored.
type componentYAML struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Type     string `yaml:"type"`
		Metadata []struct {
			Name  string `yaml:"name"`
			Value string `yaml:"value"`
		} `yaml:"metadata"`
	} `yaml:"spec"`
}

// readComponents parses every *.yaml/*.yml file directly under dir into a
// DaprComponent. It is best-effort, mirroring listNames: a missing directory
// yields nil, and an unreadable or unparseable file is skipped rather than
// failing the whole response. Results are sorted by Name for determinism.
func readComponents(dir string) []DaprComponent {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var comps []DaprComponent
	for _, ent := range ents {
		if ent.IsDir() || !isYAML(ent.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		var doc componentYAML
		if err := yaml.Unmarshal(data, &doc); err != nil {
			continue
		}
		name := doc.Metadata.Name
		if name == "" {
			name = stem(ent.Name())
		}
		comps = append(comps, DaprComponent{
			Name:      name,
			Type:      doc.Spec.Type,
			Category:  classify(doc.Spec.Type),
			Direction: direction(doc),
			File:      ent.Name(),
		})
	}

	sort.Slice(comps, func(i, j int) bool { return comps[i].Name < comps[j].Name })
	return comps
}

// classify maps a Dapr spec.type to the coarse category the flow canvas groups
// by: the segment before the first "." ("pubsub.in-memory" -> "pubsub"). An
// empty or dot-less type falls back to "other".
func classify(specType string) string {
	prefix, _, ok := strings.Cut(specType, ".")
	if !ok || prefix == "" {
		return "other"
	}
	switch prefix {
	case "pubsub", "state", "bindings":
		if prefix == "bindings" {
			return "binding"
		}
		return prefix
	default:
		return "other"
	}
}

// direction returns the component's "direction" spec.metadata entry
// normalized to "input", "output" or "input,output" (Dapr accepts a comma
// list in either order, with arbitrary spacing and casing). An absent entry
// or any unrecognized value yields "" — unknown is surfaced, not guessed.
func direction(doc componentYAML) string {
	for _, m := range doc.Spec.Metadata {
		if m.Name == "direction" {
			return normalizeDirection(m.Value)
		}
	}
	return ""
}

func normalizeDirection(raw string) string {
	if raw == "" {
		return ""
	}
	var in, out bool
	for tok := range strings.SplitSeq(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "input":
			in = true
		case "output":
			out = true
		default:
			return ""
		}
	}
	switch {
	case in && out:
		return "input,output"
	case in:
		return "input"
	default:
		return "output"
	}
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// stem returns a filename without its extension.
func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}
