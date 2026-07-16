package blueprint

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakePrompter struct {
	answers map[string]any
}

func (p *fakePrompter) Prompt(f FieldSpec) (any, error) {
	if ans, ok := p.answers[f.Name]; ok {
		return ans, nil
	}
	return nil, errors.New("unexpected prompt for " + f.Name)
}

// buildTemplate constructs a *Template with the supplied parameters block,
// preserving property declaration order so Fields() returns them in the
// caller-supplied sequence.
func buildTemplate(parameters map[string]any, order []string, values map[string]string) *Template {
	return &Template{
		APIVersion: APIVersionV1,
		Kind:       KindTemplate,
		Metadata:   Metadata{Name: "test"},
		Spec: Spec{
			Parameters:     parameters,
			Values:         values,
			parameterOrder: order,
		},
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	vf := filepath.Join(dir, "vals.yaml")
	if err := os.WriteFile(vf, []byte("namespace: from-file\nintegrationName: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"integrationName"},
		"properties": map[string]any{
			"integrationName": map[string]any{"type": "string"},
			"namespace":       map[string]any{"type": "string", "default": "default"},
			"region":          map[string]any{"type": "string", "default": "eu-north-1"},
		},
	}, []string{"integrationName", "namespace", "region"}, nil)

	sets := map[string]any{"integrationName": "from-set"}
	out, err := Resolve(tmpl, []string{vf}, nil, sets, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out["integrationName"] != "from-set" {
		t.Errorf("integrationName = %v, want from-set", out["integrationName"])
	}
	if out["namespace"] != "from-file" {
		t.Errorf("namespace = %v, want from-file", out["namespace"])
	}
	if out["region"] != "eu-north-1" {
		t.Errorf("region = %v, want eu-north-1", out["region"])
	}
}

func TestResolveLayeredPrecedence(t *testing.T) {
	dir := t.TempDir()
	vf := filepath.Join(dir, "vals.yaml")
	if err := os.WriteFile(vf, []byte("namespace: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"integrationName"},
		"properties": map[string]any{
			"integrationName": map[string]any{"type": "string"},
			"namespace":       map[string]any{"type": "string", "default": "default"},
			"region":          map[string]any{"type": "string", "default": "eu-north-1"},
			"replicas":        map[string]any{"type": "integer", "default": 1},
		},
	}, []string{"integrationName", "namespace", "region", "replicas"}, nil)

	// base beats defaults, files beat base, sets beat files. A base value for
	// a required parameter satisfies it (no prompter needed), and base values
	// are coerced to the declared schema type like every other layer.
	base := map[string]any{
		"integrationName": "from-base",
		"namespace":       "from-base",
		"region":          "from-base",
		"replicas":        "3",
	}
	out, err := ResolveLayered(tmpl, base, []string{vf}, nil, map[string]any{"region": "from-set"}, nil)
	if err != nil {
		t.Fatalf("ResolveLayered: %v", err)
	}
	if out["integrationName"] != "from-base" {
		t.Errorf("integrationName = %v, want from-base (base satisfies required)", out["integrationName"])
	}
	if out["namespace"] != "from-file" {
		t.Errorf("namespace = %v, want from-file (files beat base)", out["namespace"])
	}
	if out["region"] != "from-set" {
		t.Errorf("region = %v, want from-set (sets beat base)", out["region"])
	}
	if out["replicas"] != int64(3) {
		t.Errorf("replicas = %v (%T), want int64(3) (base values coerced)", out["replicas"], out["replicas"])
	}
}

func TestResolveMissingRequiredNoPrompter(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "string"},
			"c": map[string]any{"type": "string", "default": "x"},
		},
	}, []string{"a", "b", "c"}, nil)

	_, err := Resolve(tmpl, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("error should name both missing parameters: %v", err)
	}
}

func TestResolveUsesPrompter(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"a"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
	}, []string{"a"}, nil)

	p := &fakePrompter{answers: map[string]any{"a": "prompted"}}
	out, err := Resolve(tmpl, nil, nil, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	if out["a"] != "prompted" {
		t.Errorf("a = %v", out["a"])
	}
}

func TestResolveEmptyPromptIsError(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"a"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
	}, []string{"a"}, nil)

	p := &fakePrompter{answers: map[string]any{"a": ""}}
	if _, err := Resolve(tmpl, nil, nil, nil, p); err == nil {
		t.Fatal("expected error for empty prompt answer on required parameter")
	}
}

func TestResolveCoercesSetForTypedParameters(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean"},
			"count":   map[string]any{"type": "integer"},
		},
	}, []string{"enabled", "count"}, nil)

	sets := map[string]any{"enabled": "true", "count": "5"}
	out, err := Resolve(tmpl, nil, nil, sets, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out["enabled"] != true {
		t.Errorf("enabled = %v (%T), want true (bool)", out["enabled"], out["enabled"])
	}
	if out["count"] != int64(5) {
		t.Errorf("count = %v (%T), want 5 (int64)", out["count"], out["count"])
	}
}

func TestResolveValidatesPattern(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{
				"type":    "string",
				"pattern": "^[a-z]+$",
			},
		},
	}, []string{"name"}, nil)

	sets := map[string]any{"name": "Has-Dashes"}
	if _, err := Resolve(tmpl, nil, nil, sets, nil); err == nil {
		t.Fatal("expected pattern validation error")
	}
}

func TestResolveDerivedValues(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"system", "sourceSystem"},
		"properties": map[string]any{
			"system":       map[string]any{"type": "string"},
			"sourceSystem": map[string]any{"type": "string"},
		},
	}, []string{"system", "sourceSystem"}, map[string]string{
		"name": "{{ .system }}-{{ .sourceSystem }}-extractor",
	})

	sets := map[string]any{"system": "orders", "sourceSystem": "salesforce"}
	out, err := Resolve(tmpl, nil, nil, sets, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out["name"] != "orders-salesforce-extractor" {
		t.Errorf("derived name = %v", out["name"])
	}
}

func TestResolveRejectsDerivedNameCollision(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"foo": map[string]any{"type": "string", "default": "x"},
		},
	}, []string{"foo"}, map[string]string{
		"foo": "{{ .foo }}-derived",
	})

	if _, err := Resolve(tmpl, nil, nil, nil, nil); err == nil {
		t.Fatal("expected error for derived value colliding with parameter")
	}
}

func TestParseSets(t *testing.T) {
	out, err := ParseSets([]string{"k=v", "a=b=c"})
	if err != nil {
		t.Fatal(err)
	}
	if out["k"] != "v" {
		t.Errorf("k = %v", out["k"])
	}
	if out["a"] != "b=c" {
		t.Errorf("a = %v, want b=c (only first = splits)", out["a"])
	}
}

func TestParseSetsInvalid(t *testing.T) {
	cases := []string{"nokey", "=novalue"}
	for _, c := range cases {
		if _, err := ParseSets([]string{c}); err == nil {
			t.Errorf("ParseSets(%q) should fail", c)
		}
	}
}

func TestResolveReadsStdinValues(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"integrationName"},
		"properties": map[string]any{
			"integrationName": map[string]any{"type": "string"},
			"enabled":         map[string]any{"type": "boolean", "default": false},
		},
	}, []string{"integrationName", "enabled"}, nil)

	// JSON on stdin — verifies typed values survive without coercion.
	stdin := bytes.NewBufferString(`{"integrationName": "from-stdin", "enabled": true}`)
	out, err := Resolve(tmpl, []string{StdinValuesPath}, stdin, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out["integrationName"] != "from-stdin" {
		t.Errorf("integrationName = %v", out["integrationName"])
	}
	if out["enabled"] != true {
		t.Errorf("enabled = %v (%T), want true (bool)", out["enabled"], out["enabled"])
	}
}

func TestResolveStdinLayersAfterFiles(t *testing.T) {
	dir := t.TempDir()
	vf := filepath.Join(dir, "vals.yaml")
	if err := os.WriteFile(vf, []byte("integrationName: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"integrationName"},
		"properties": map[string]any{
			"integrationName": map[string]any{"type": "string"},
		},
	}, []string{"integrationName"}, nil)

	stdin := bytes.NewBufferString(`integrationName: from-stdin`)
	out, err := Resolve(tmpl, []string{vf, StdinValuesPath}, stdin, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out["integrationName"] != "from-stdin" {
		t.Errorf("integrationName = %v, want from-stdin (stdin should override file)", out["integrationName"])
	}
}

func TestResolveSetsOverrideStdin(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type":     "object",
		"required": []any{"integrationName"},
		"properties": map[string]any{
			"integrationName": map[string]any{"type": "string"},
		},
	}, []string{"integrationName"}, nil)

	stdin := bytes.NewBufferString(`{"integrationName": "from-stdin"}`)
	sets := map[string]any{"integrationName": "from-set"}
	out, err := Resolve(tmpl, []string{StdinValuesPath}, stdin, sets, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out["integrationName"] != "from-set" {
		t.Errorf("integrationName = %v, want from-set (--set should override stdin)", out["integrationName"])
	}
}

func TestResolveStdinDuplicateRejected(t *testing.T) {
	tmpl := buildTemplate(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string", "default": "x"},
		},
	}, []string{"a"}, nil)

	stdin := bytes.NewBufferString(`{}`)
	_, err := Resolve(tmpl, []string{StdinValuesPath, StdinValuesPath}, stdin, nil, nil)
	if err == nil {
		t.Fatal("expected error when stdin is requested twice")
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Errorf("error should mention stdin: %v", err)
	}
}
