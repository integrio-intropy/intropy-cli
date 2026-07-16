package template

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	APIVersionV1 = "intropy.dev/v1"
	KindTemplate = "Template"
)

// Template is the on-disk manifest for a template. The envelope mirrors the
// Kubernetes / Backstage shape so authors recognize it at a glance; the body
// is a deliberately narrow subset (no ui: extensions, no step pipeline).
type Template struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name        string            `yaml:"name"`
	Title       string            `yaml:"title,omitempty"`
	Description string            `yaml:"description,omitempty"`
	Tags        []string          `yaml:"tags,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
}

type Spec struct {
	Parameters map[string]any    `yaml:"parameters"`
	Values     map[string]string `yaml:"values,omitempty"`

	// parameterOrder captures the declaration order of properties in
	// spec.parameters.properties, since Go maps don't preserve YAML order.
	// Populated by UnmarshalYAML.
	parameterOrder []string
}

// UnmarshalYAML decodes the spec and captures property declaration order
// so Fields() can return FieldSpecs in author-intended sequence.
func (s *Spec) UnmarshalYAML(node *yaml.Node) error {
	type rawSpec struct {
		Parameters map[string]any    `yaml:"parameters"`
		Values     map[string]string `yaml:"values,omitempty"`
	}
	var r rawSpec
	if err := node.Decode(&r); err != nil {
		return err
	}
	s.Parameters = r.Parameters
	s.Values = r.Values
	s.parameterOrder = extractPropertyOrder(node)
	return nil
}

func extractPropertyOrder(specNode *yaml.Node) []string {
	if specNode.Kind != yaml.MappingNode {
		return nil
	}
	paramsNode := childByKey(specNode, "parameters")
	if paramsNode == nil {
		return nil
	}
	propsNode := childByKey(paramsNode, "properties")
	if propsNode == nil || propsNode.Kind != yaml.MappingNode {
		return nil
	}
	order := make([]string, 0, len(propsNode.Content)/2)
	for i := 0; i < len(propsNode.Content); i += 2 {
		order = append(order, propsNode.Content[i].Value)
	}
	return order
}

func childByKey(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// FieldSpec is the prompter/CLI view of a single property in spec.parameters.
// Prompters and form code only consume FieldSpecs — they never touch the raw
// JSON Schema map.
type FieldSpec struct {
	Name        string
	Title       string
	Description string
	Type        string // "string" | "boolean" | "integer" | "number"
	Enum        []any
	Default     any
	Pattern     string
	Required    bool
}

// Fields returns the JSON Schema properties as FieldSpecs in YAML declaration
// order.
func (t *Template) Fields() []FieldSpec {
	props, _ := t.Spec.Parameters["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	required := map[string]bool{}
	if list, ok := t.Spec.Parameters["required"].([]any); ok {
		for _, r := range list {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	out := make([]FieldSpec, 0, len(t.Spec.parameterOrder))
	for _, name := range t.Spec.parameterOrder {
		raw, _ := props[name].(map[string]any)
		out = append(out, fieldFromSchema(name, raw, required[name]))
	}
	return out
}

func fieldFromSchema(name string, schema map[string]any, required bool) FieldSpec {
	f := FieldSpec{Name: name, Required: required}
	if schema == nil {
		return f
	}
	f.Type, _ = schema["type"].(string)
	f.Title, _ = schema["title"].(string)
	f.Description, _ = schema["description"].(string)
	f.Pattern, _ = schema["pattern"].(string)
	f.Default = schema["default"]
	if e, ok := schema["enum"].([]any); ok {
		f.Enum = e
	}
	return f
}

// LoadTemplate reads and validates a template.yaml file.
func LoadTemplate(path string) (*Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	var t Template
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	if err := t.validate(); err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}
	return &t, nil
}

func (t *Template) validate() error {
	if t.APIVersion != APIVersionV1 {
		return fmt.Errorf("unsupported apiVersion %q (want %q)", t.APIVersion, APIVersionV1)
	}
	if t.Kind != KindTemplate {
		return fmt.Errorf("unsupported kind %q (want %q)", t.Kind, KindTemplate)
	}
	if t.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if t.Spec.Parameters == nil {
		return fmt.Errorf("spec.parameters is required")
	}
	if typ, _ := t.Spec.Parameters["type"].(string); typ != "object" {
		return fmt.Errorf(`spec.parameters.type must be "object"`)
	}
	return nil
}
