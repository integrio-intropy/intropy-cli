package template

import (
	"fmt"
	"os"
	"regexp"

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
	Parameters   map[string]any    `yaml:"parameters"`
	Values       map[string]string `yaml:"values,omitempty"`
	Files        []FileRule        `yaml:"files,omitempty"`
	Dependencies []DependencySpec  `yaml:"dependencies,omitempty"`

	// Local declares what a local-cluster render of this template offers.
	// Absent on templates that are never rendered locally; on deploy-component
	// it is how the fixture catalog travels with the release rather than being
	// hardcoded in the CLI.
	Local *LocalSpec `yaml:"local,omitempty"`

	// Bindings is the closed catalog of Dapr binding types a connector can
	// deploy as in a GitOps environment — the menu deploy init asks its
	// connector question from, and what recorded answers are validated
	// against. Absent on templates without connectors, and on releases older
	// than the question: the CLI then falls back to placeholders.
	Bindings []string `yaml:"bindings,omitempty"`

	// parameterOrder captures the declaration order of properties in
	// spec.parameters.properties, since Go maps don't preserve YAML order.
	// Populated by UnmarshalYAML.
	parameterOrder []string
}

// FileRule conditionally includes part of a skeleton, so one template can serve
// platforms whose manifest sets differ rather than only whose values differ.
//
// Path is a slash-separated glob relative to skeleton/, matched against the
// *source* path with any .tmpl suffix included — a rule that decides on values
// cannot depend on a path those values produce, so a templated directory segment
// is only reachable via a glob. A trailing "/**" also matches everything beneath
// the directory, and prunes it before its contents are parsed.
//
// When is a Go template (sprig available) rendered against the resolved values.
// Any result other than "", "false" or "0" includes the match.
//
// The first rule whose Path matches decides, and a path no rule matches is
// included — so a template without spec.files renders exactly as it did before
// this field existed.
type FileRule struct {
	Path string `yaml:"path" json:"path"`
	When string `yaml:"when" json:"when"`
}

// DependencySpec declares another template in the same library that must
// exist as a sibling of this template's output. Create renders it when the
// target directory is missing and skips it when a scaffold record from the
// same template is already there, so a shared project is created exactly
// once no matter how many components declare it.
type DependencySpec struct {
	// Template is the dependency's directory name in the templates repo.
	Template string `yaml:"template" json:"template"`
	// Output is a Go template (with sprig) rendered against the parent's
	// resolved values. It must produce a single path segment; the dependency
	// is created as a sibling of the parent's output directory.
	Output string `yaml:"output" json:"output"`
	// Values maps dependency parameter names to Go templates rendered
	// against the parent's resolved values. Dependencies resolve without
	// prompting, so together with the dependency's own defaults these must
	// cover every required parameter.
	Values map[string]string `yaml:"values,omitempty" json:"values,omitempty"`
}

// LocalSpec is the local-render section of a template manifest.
//
// Fixtures is the closed catalog of fixture bindings a connector can be bound
// to in a local render. The CLI reads it from the fetched library, so the menu
// it presents, the values it accepts and the skeletons it renders all come from
// one release of one repository.
type LocalSpec struct {
	Fixtures []string `yaml:"fixtures"`
}

// fixtureNamePattern keeps a fixture name usable as a path segment, a Dapr
// component name fragment and a map key without escaping anywhere.
var fixtureNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// UnmarshalYAML decodes the spec and captures property declaration order
// so Fields() can return FieldSpecs in author-intended sequence.
func (s *Spec) UnmarshalYAML(node *yaml.Node) error {
	// Every field of Spec must be repeated here: the decode targets rawSpec, so
	// a field added to Spec alone is silently dropped.
	type rawSpec struct {
		Parameters   map[string]any    `yaml:"parameters"`
		Values       map[string]string `yaml:"values,omitempty"`
		Files        []FileRule        `yaml:"files,omitempty"`
		Dependencies []DependencySpec  `yaml:"dependencies,omitempty"`
		Local        *LocalSpec        `yaml:"local,omitempty"`
		Bindings     []string          `yaml:"bindings,omitempty"`
	}
	var r rawSpec
	if err := node.Decode(&r); err != nil {
		return err
	}
	s.Parameters = r.Parameters
	s.Values = r.Values
	s.Files = r.Files
	s.Dependencies = r.Dependencies
	s.Local = r.Local
	s.Bindings = r.Bindings
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
	for i, rule := range t.Spec.Files {
		if rule.Path == "" {
			return fmt.Errorf("spec.files[%d]: path is required", i)
		}
		// A rule with no condition either does nothing or means the author
		// forgot the condition; neither deserves to render.
		if rule.When == "" {
			return fmt.Errorf("spec.files[%d] (%s): when is required", i, rule.Path)
		}
		// Parsed here so a syntax error surfaces at load time rather than
		// partway through a render.
		if _, err := compileExpr(rule.When); err != nil {
			return fmt.Errorf("spec.files[%d] (%s): invalid when: %w", i, rule.Path, err)
		}
	}
	for i, dep := range t.Spec.Dependencies {
		if err := validateTemplateName(dep.Template); err != nil {
			return fmt.Errorf("spec.dependencies[%d]: %w", i, err)
		}
		if dep.Output == "" {
			return fmt.Errorf("spec.dependencies[%d] (%s): output is required", i, dep.Template)
		}
	}
	if t.Spec.Local != nil {
		seen := make(map[string]bool, len(t.Spec.Local.Fixtures))
		for i, fx := range t.Spec.Local.Fixtures {
			if !fixtureNamePattern.MatchString(fx) {
				return fmt.Errorf("spec.local.fixtures[%d]: %q is not a valid fixture name (lowercase letters, digits and dashes, starting with a letter or digit)", i, fx)
			}
			if seen[fx] {
				return fmt.Errorf("spec.local.fixtures[%d]: %q is declared twice", i, fx)
			}
			seen[fx] = true
		}
	}
	if err := validateCatalog("spec.bindings", t.Spec.Bindings); err != nil {
		return err
	}
	return nil
}

// validateCatalog applies the fixture-name rules to any closed catalog the
// manifest declares: a name must work as a Dapr binding type suffix and a
// map key without escaping anywhere, and be declared once.
func validateCatalog(field string, entries []string) error {
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if !fixtureNamePattern.MatchString(e) {
			return fmt.Errorf("%s[%d]: %q is not a valid binding name (lowercase letters, digits and dashes, starting with a letter or digit)", field, i, e)
		}
		if seen[e] {
			return fmt.Errorf("%s[%d]: %q is declared twice", field, i, e)
		}
		seen[e] = true
	}
	return nil
}
