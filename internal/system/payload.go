package system

import (
	"fmt"
	"path/filepath"
)

// buildPayload assembles the value map passed as SetValues to the
// system-host template. The template renders every declaration file from
// it, so the payload is join-free: each component carries the Topics and
// Connectors field identifiers it touches, resolved here from the model so
// the template stays a flat range over the lists.
func buildPayload(m *Model, outputDir, kebab string) (map[string]any, error) {
	topics := make([]any, len(m.Topics))
	fieldByKey := map[TopicKey]string{}
	for i, t := range m.Topics {
		fieldByKey[t.TopicKey] = t.Field
		topics[i] = map[string]any{
			"pubsub":   t.Pubsub,
			"name":     t.Name,
			"contract": t.Contract,
			"field":    t.Field,
		}
	}

	connectors := make([]any, len(m.Connectors))
	connectorField := map[string]string{}
	for i, c := range m.Connectors {
		connectorField[c.Name] = c.Field
		connectors[i] = map[string]any{
			"name":  c.Name,
			"field": c.Field,
		}
	}

	components := make([]any, len(m.Components))
	for i, c := range m.Components {
		// Kind is verbatim: Assemble already validated it against the
		// parse registry. The wiring fields follow the component's
		// shape, not its kind — a topic emits topicField, one connector
		// connectorField, two connectors fromField/toField.
		entry := map[string]any{
			"appId": c.AppID,
			"kind":  c.Kind,
		}
		if c.Topic != nil {
			entry["topicField"] = fieldByKey[*c.Topic]
		}
		switch len(c.Connectors) {
		case 0:
			if c.Topic != nil {
				entry["connectorField"] = "" // a topic block without a connector
			}
		case 1:
			entry["connectorField"] = connectorField[c.Connectors[0]]
		default:
			entry["fromField"] = connectorField[c.Connectors[0]]
			entry["toField"] = connectorField[c.Connectors[1]]
		}
		components[i] = entry
	}

	include, err := contractsInclude(outputDir, m.Shared)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"name":       kebab,
		"topics":     topics,
		"connectors": connectors,
		"components": components,
		"sharedContracts": map[string]any{
			"name":    m.Shared.Name,
			"include": include,
		},
	}, nil
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
