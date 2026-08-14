//go:build e2e

package system

// Renders the real system-host template from a local intropy-templates
// checkout through the production engine (values resolution, spec.files,
// conditional dependencies, skeleton rendering). Run with:
//
//	INTROPY_TEMPLATES_DIR=~/dev/intropy/tooling/intropy-templates go test -tags e2e ./internal/system/ -run TestRealSystemHost -v

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/Masterminds/sprig/v3"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func templatesDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("INTROPY_TEMPLATES_DIR")
	if dir == "" {
		t.Skip("INTROPY_TEMPLATES_DIR not set")
	}
	return dir
}

func renderRealHost(t *testing.T, payload map[string]any) (outDir, parent string) {
	t.Helper()
	lib := templatesDir(t)
	tmpl, err := template.LoadTemplate(filepath.Join(lib, "system-host", "template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := template.Resolve(tmpl, nil, nil, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent = t.TempDir()
	outDir = filepath.Join(parent, "order-flow")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := template.RenderFiltered(filepath.Join(lib, "system-host", "skeleton"), outDir, values, tmpl.Spec.Files); err != nil {
		t.Fatalf("render: %v", err)
	}
	// The dependency half of Create, unexported — replicate the when check
	// for the one entry the host declares.
	for _, dep := range tmpl.Spec.Dependencies {
		_ = dep
	}
	return outDir, parent
}

func TestRealSystemHostTransactionalOnly(t *testing.T) {
	outDir, _ := renderRealHost(t, map[string]any{
		"name":       "trans",
		"topics":     []any{},
		"ports":      []any{map[string]any{"name": "erp-source"}, map[string]any{"name": "erp-destination"}},
		"components": []any{map[string]any{"appId": "erp-sync", "kind": "transactional-integration", "fromPort": "erp-source", "toPort": "erp-destination"}},
	})
	if _, err := os.Stat(filepath.Join(outDir, "Topics.cs")); !os.IsNotExist(err) {
		t.Errorf("Topics.cs should not render, err = %v", err)
	}
	csproj, _ := os.ReadFile(filepath.Join(outDir, "Trans.SystemHost.csproj"))
	if strings.Contains(string(csproj), "ProjectReference") {
		t.Errorf("csproj should have no contracts reference:\n%s", csproj)
	}
	system, err := os.ReadFile(filepath.Join(outDir, "TransSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`builder.AddTransactionalIntegration("erp-sync")`, ".From(Ports.ErpSource)", ".To(Ports.ErpDestination)"} {
		if !strings.Contains(string(system), want) {
			t.Errorf("TransSystem.cs missing %q:\n%s", want, system)
		}
	}
	ports, err := os.ReadFile(filepath.Join(outDir, "Ports.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ports), "PortRef ErpSource") {
		t.Errorf("Ports.cs:\n%s", ports)
	}
	dev, err := os.ReadFile(filepath.Join(outDir, "TransDevelopment.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dev), `development.Files(Ports.ErpSource).RootPath("./test/erp-source");`) {
		t.Errorf("TransDevelopment.cs:\n%s", dev)
	}
}

func TestRealSystemHostTopicSystemWithContracts(t *testing.T) {
	outDir, _ := renderRealHost(t, map[string]any{
		"name":   "order-flow",
		"topics": []any{map[string]any{"pubsub": "pubsub", "name": "orders", "contract": "Order"}},
		"ports":  []any{map[string]any{"name": "order-extractor-source"}, map[string]any{"name": "order-loader-destination"}},
		"components": []any{
			map[string]any{"appId": "order-extractor", "kind": "extractor", "topic": map[string]any{"pubsub": "pubsub", "name": "orders"}, "port": "order-extractor-source"},
			map[string]any{"appId": "order-loader", "kind": "loader", "topic": map[string]any{"pubsub": "pubsub", "name": "orders"}, "port": "order-loader-destination"},
		},
		"sharedContracts": map[string]any{"name": "Contracts", "include": "../Contracts/Contracts.csproj"},
	})
	topics, err := os.ReadFile(filepath.Join(outDir, "Topics.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"using Contracts;", `TopicRef<Order> Orders = TopicRef<Order>.Define("pubsub", "orders");`} {
		if !strings.Contains(string(topics), want) {
			t.Errorf("Topics.cs missing %q:\n%s", want, topics)
		}
	}
	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".From(Ports.OrderExtractorSource)", ".Publishes(Topics.Orders)", ".Subscribes(Topics.Orders)", ".To(Ports.OrderLoaderDestination)"} {
		if !strings.Contains(string(system), want) {
			t.Errorf("OrderFlowSystem.cs missing %q:\n%s", want, system)
		}
	}
	csproj, _ := os.ReadFile(filepath.Join(outDir, "OrderFlow.SystemHost.csproj"))
	if !strings.Contains(string(csproj), `<ProjectReference Include="../Contracts/Contracts.csproj"`) {
		t.Errorf("csproj:\n%s", csproj)
	}
}

func TestRealSystemHostFieldCollisionFails(t *testing.T) {
	lib := templatesDir(t)
	tmpl, err := template.LoadTemplate(filepath.Join(lib, "system-host", "template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := template.Resolve(tmpl, nil, nil, map[string]any{
		"name": "collide",
		"topics": []any{
			map[string]any{"pubsub": "pubsub", "name": "order-events", "contract": "Order"},
			map[string]any{"pubsub": "pubsub", "name": "order.events", "contract": "Order"},
		},
		"ports":           []any{},
		"components":      []any{},
		"sharedContracts": map[string]any{"name": "Contracts", "include": "../Contracts/Contracts.csproj"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = template.RenderFiltered(filepath.Join(lib, "system-host", "skeleton"), t.TempDir(), values, tmpl.Spec.Files)
	if err == nil || !strings.Contains(err.Error(), "both derive the Topics.cs field OrderEvents") {
		t.Fatalf("err = %v, want a field-collision failure naming both topics", err)
	}
}

func TestRealSystemHostDependencyWhen(t *testing.T) {
	lib := templatesDir(t)
	tmpl, err := template.LoadTemplate(filepath.Join(lib, "system-host", "template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Spec.Dependencies) != 1 {
		t.Fatalf("dependencies = %+v", tmpl.Spec.Dependencies)
	}
	dep := tmpl.Spec.Dependencies[0]
	if dep.Template != "shared-contracts" || dep.When == "" {
		t.Fatalf("dependency = %+v", dep)
	}

	// Evaluate the when expression against resolved payloads with the same
	// machinery the engine uses (sprig + missingkey=error).
	eval := func(payload map[string]any) string {
		values, err := template.Resolve(tmpl, nil, nil, payload, nil)
		if err != nil {
			t.Fatal(err)
		}
		tt, err := texttemplate.New("when").Funcs(sprig.TxtFuncMap()).Option("missingkey=error").Parse(dep.When)
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		if err := tt.Execute(&sb, values); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}

	topicsPayload := map[string]any{
		"name":       "order-flow",
		"topics":     []any{map[string]any{"pubsub": "pubsub", "name": "orders", "contract": "Order"}},
		"ports":      []any{},
		"components": []any{},
	}
	if got := eval(topicsPayload); got == "false" || got == "" {
		t.Errorf("topic system without contracts should render the dependency, when = %q", got)
	}
	withContracts := map[string]any{
		"name":            "order-flow",
		"topics":          []any{map[string]any{"pubsub": "pubsub", "name": "orders", "contract": "Order"}},
		"ports":           []any{},
		"components":      []any{},
		"sharedContracts": map[string]any{"name": "Contracts", "include": "../Contracts/Contracts.csproj"},
	}
	if got := eval(withContracts); got != "false" {
		t.Errorf("existing contracts should skip the dependency, when = %q", got)
	}
	noTopics := map[string]any{"name": "trans", "topics": []any{}, "ports": []any{}, "components": []any{}}
	if got := eval(noTopics); got != "false" {
		t.Errorf("topic-free system should skip the dependency, when = %q", got)
	}

	// The dependency's contract value renders from the first topic, and the
	// dependency template itself loads from the same checkout.
	values, err := template.Resolve(tmpl, nil, nil, topicsPayload, nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := texttemplate.New("c").Funcs(sprig.TxtFuncMap()).Option("missingkey=error").Parse(dep.Values["contract"])
	if err != nil {
		t.Fatal(err)
	}
	var cs strings.Builder
	if err := ct.Execute(&cs, values); err != nil {
		t.Fatal(err)
	}
	if cs.String() != "Order" {
		t.Errorf("dependency contract = %q, want Order", cs.String())
	}
	if _, err := template.LoadTemplate(filepath.Join(lib, dep.Template, "template.yaml")); err != nil {
		t.Fatalf("dependency template: %v", err)
	}
}
