package template

import (
	"strings"
	"testing"
)

func reservedTestTemplate() *Template {
	return buildTemplate(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":      map[string]any{"type": "string"},
			"namespace": map[string]any{"type": "string", "default": "integrations"},
		},
	}, []string{"name", "namespace"}, map[string]string{"imageTag": "unpinned"})
}

func TestInjectReserved(t *testing.T) {
	tmpl := reservedTestTemplate()
	values := map[string]any{"name": "order-extract"}

	reserved := map[string]any{
		ReservedTopologyKey: map[string]any{
			"connectors": []any{map[string]any{"name": "erp"}},
		},
		ReservedGitopsKey: map[string]any{"domain": "sales"},
	}
	if err := InjectReserved(tmpl, values, reserved); err != nil {
		t.Fatalf("InjectReserved: %v", err)
	}

	topo, ok := values[ReservedTopologyKey].(map[string]any)
	if !ok {
		t.Fatalf("topology = %T, want map", values[ReservedTopologyKey])
	}
	if len(topo["connectors"].([]any)) != 1 {
		t.Errorf("connectors = %v", topo["connectors"])
	}
	if values["name"] != "order-extract" {
		t.Errorf("existing value clobbered: %v", values["name"])
	}
}

// A reserved key that shadows a declared parameter would let a template read
// one thing while the schema documented another.
func TestInjectReservedRejectsParameterCollision(t *testing.T) {
	tmpl := reservedTestTemplate()
	values := map[string]any{"name": "order-extract"}

	err := InjectReserved(tmpl, values, map[string]any{"namespace": map[string]any{}})
	if err == nil {
		t.Fatal("expected an error for a parameter collision")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error does not name the key: %v", err)
	}
}

func TestInjectReservedRejectsDerivedValueCollision(t *testing.T) {
	tmpl := reservedTestTemplate()
	if err := InjectReserved(tmpl, map[string]any{}, map[string]any{"imageTag": "whatever"}); err == nil {
		t.Fatal("expected an error for a spec.values collision")
	}
}

// --set accepts undeclared keys, so a stray one could otherwise be silently
// replaced by injected data.
func TestInjectReservedRejectsExistingValue(t *testing.T) {
	tmpl := reservedTestTemplate()
	values := map[string]any{ReservedTopologyKey: "set-by-hand"}

	if err := InjectReserved(tmpl, values, map[string]any{ReservedTopologyKey: map[string]any{}}); err == nil {
		t.Fatal("expected an error when the key is already present in values")
	}
}

// Injection happens after schema validation on purpose: the structures it adds
// are not expressible as scalar parameters, so validating them would fail.
func TestInjectReservedAcceptsStructuresTheSchemaWouldReject(t *testing.T) {
	tmpl := reservedTestTemplate()
	values, err := Resolve(tmpl, nil, nil, map[string]any{"name": "order-extract"}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	reserved := map[string]any{
		ReservedComponentKey: map[string]any{"workload": "cronjob", "topics": []any{"orders"}},
	}
	if err := InjectReserved(tmpl, values, reserved); err != nil {
		t.Fatalf("InjectReserved: %v", err)
	}
	if values["imageTag"] != "unpinned" {
		t.Errorf("derived value lost: %v", values["imageTag"])
	}
	comp := values[ReservedComponentKey].(map[string]any)
	if comp["workload"] != "cronjob" {
		t.Errorf("component.workload = %v", comp["workload"])
	}
}

// Nothing is injected when a later key collides, so a caller never sees a
// half-populated value map.
func TestInjectReservedIsAllOrNothing(t *testing.T) {
	tmpl := reservedTestTemplate()
	values := map[string]any{}

	reserved := map[string]any{
		ReservedTopologyKey: map[string]any{"system": "ordersync"},
		"namespace":         map[string]any{},
	}
	if err := InjectReserved(tmpl, values, reserved); err == nil {
		t.Fatal("expected an error for a parameter collision")
	}
	if _, present := values[ReservedTopologyKey]; present {
		t.Errorf("topology was injected despite the error: %v", values)
	}
}
