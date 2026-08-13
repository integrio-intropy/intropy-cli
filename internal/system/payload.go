package system

import (
	"fmt"
	"path/filepath"
)

// buildPayload assembles the value map passed as SetValues to the
// system-host template. The payload carries workspace facts only — topic and
// connector names, component wiring, the detected contracts sibling — and
// the template derives everything that exists only inside the generated
// files: the Topics/Connectors field identifiers, the joins between
// components and those fields, and the csproj ProjectReference path.
//
// The contracts sibling travels as a relative include: the template renders
// it verbatim into the host csproj's ProjectReference, and the host is
// always created as a sibling of the contracts project, so the CLI's
// workspace-relative path and the include differ only by the leading "../".
// Computing the include here keeps the templates (and the test fixtures that
// mirror them) free of host-side path arithmetic.
func buildPayload(m *Model, outputDir, kebab string) (map[string]any, error) {
	topics := make([]any, len(m.Topics))
	for i, t := range m.Topics {
		topics[i] = map[string]any{
			"pubsub":   t.Pubsub,
			"name":     t.Name,
			"contract": t.Contract,
		}
	}

	connectors := make([]any, len(m.Connectors))
	for i, c := range m.Connectors {
		connectors[i] = map[string]any{
			"name": c.Name,
		}
	}

	components := make([]any, len(m.Components))
	for i, c := range m.Components {
		// Kind is verbatim: Assemble already validated it against the
		// parse registry. The wiring fields follow the component's shape,
		// not its kind — a topic carries topic, one connector connector,
		// two connectors from/to — and the template resolves each name to
		// the field identifier it derived for the topic or connector.
		entry := map[string]any{
			"appId": c.AppID,
			"kind":  c.Kind,
		}
		if c.Topic != nil {
			entry["topic"] = map[string]any{
				"pubsub": c.Topic.Pubsub,
				"name":   c.Topic.Name,
			}
		}
		switch len(c.Connectors) {
		case 0:
		case 1:
			entry["connector"] = c.Connectors[0]
		default:
			entry["from"] = c.Connectors[0]
			entry["to"] = c.Connectors[1]
		}
		components[i] = entry
	}

	payload := map[string]any{
		"name":       kebab,
		"topics":     topics,
		"connectors": connectors,
		"components": components,
	}
	if m.Shared != nil {
		include, err := contractsInclude(outputDir, *m.Shared)
		if err != nil {
			return nil, err
		}
		payload["sharedContracts"] = map[string]any{
			"name":    m.Shared.Name,
			"include": include,
		}
	}
	return payload, nil
}

// contractsInclude computes the ProjectReference Include path from the
// host's output directory to the shared library's csproj, slash-separated.
func contractsInclude(outputDir string, shared SharedLibrary) (string, error) {
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	absShared, err := filepath.Abs(shared.Path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absOut, absShared)
	if err != nil {
		return "", fmt.Errorf("compute path from %s to shared contracts project %s: %w", outputDir, shared.Path, err)
	}
	return filepath.ToSlash(filepath.Join(rel, shared.Name+".csproj")), nil
}
