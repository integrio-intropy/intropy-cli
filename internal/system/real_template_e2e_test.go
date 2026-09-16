//go:build e2e

package system

// Renders the real system-host template from a local intropy-templates
// checkout through the production engine (values resolution, spec.files,
// skeleton rendering), plus the real-template update flow. This is the
// WS-1.3 harness Gate G1 stands on.
//
// Run with:
//
//	INTROPY_TEMPLATES_DIR=</path/to/intropy-templates> go test -tags e2e ./internal/system/ -run TestReal -v
//
// The suite pins the scalar message-first system-host template: extractors
// carry `publishes`, loaders carry `subscribes`, Messages.cs declares
// single-argument MessageRefs, and every pre-scalar payload shape fails the
// render loudly — old workspaces migrate by re-scaffolding, never by a
// fallback guess.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
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

func loadRealHostTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.LoadTemplate(filepath.Join(templatesDir(t), "system-host", "template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return tmpl
}

func renderRealHost(t *testing.T, payload map[string]any) (outDir, parent string) {
	t.Helper()
	tmpl := loadRealHostTemplate(t)
	values, err := template.Resolve(tmpl, nil, nil, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent = t.TempDir()
	outDir = filepath.Join(parent, "order-flow")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := template.RenderFiltered(filepath.Join(templatesDir(t), "system-host", "skeleton"), outDir, values, tmpl.Spec.Files); err != nil {
		t.Fatalf("render: %v", err)
	}
	return outDir, parent
}

// The shared message and its derived channel: one identity, pubsub/<name>.
const (
	msgName     = "orders"
	msgPub      = "pubsub"
	msgContract = "Order"
)

// msgEntry is one message declaration as buildPayload assembles it from a
// publishing component: the identity, its self-typed type, and the payload
// type derived from the name.
func msgEntry(name, contract, publisher string) map[string]any {
	return map[string]any{"name": name, "type": name, "contract": contract, "publisher": publisher}
}

func payloadExtractor(appID, port string) map[string]any {
	return map[string]any{"appId": appID, "kind": "extractor", "publishes": msgName, "port": port}
}

func payloadLoader(appID, port string) map[string]any {
	return map[string]any{"appId": appID, "kind": "loader", "subscribes": msgName, "port": port}
}

// messageFirstPayload is one extractor and one loader sharing one internal
// message — the payload buildPayload assembles for the gate's slice, with
// one entry per payload section as sys create writes it.
func messageFirstPayload() map[string]any {
	return map[string]any{
		"name":   "order-flow",
		"topics": []any{map[string]any{"pubsub": msgPub, "name": msgName, "contract": msgContract}},
		"messages": []any{
			msgEntry(msgName, msgContract, "order-sweep"),
		},
		"ports": []any{
			map[string]any{"name": "order-sweep-source"},
			map[string]any{"name": "order-file-loader-destination"},
		},
		"components": []any{
			payloadExtractor("order-sweep", "order-sweep-source"),
			payloadLoader("order-file-loader", "order-file-loader-destination"),
		},
	}
}

func TestRealSystemHostMessageFirstDeclaration(t *testing.T) {
	outDir, _ := renderRealHost(t, map[string]any{
		"name":            "order-flow",
		"topics":          messageFirstPayload()["topics"],
		"messages":        messageFirstPayload()["messages"],
		"ports":           messageFirstPayload()["ports"],
		"components":      messageFirstPayload()["components"],
		"sharedContracts": map[string]any{"name": "Contracts", "include": "../Contracts/Contracts.csproj"},
	})

	if _, err := os.Stat(filepath.Join(outDir, "Topics.cs")); !os.IsNotExist(err) {
		t.Errorf("Topics.cs must not render under the message-first template, err = %v", err)
	}
	messages, err := os.ReadFile(filepath.Join(outDir, "Messages.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"using Contracts;",
		`public static readonly MessageRef<Order> Orders = MessageRef<Order>.Define("orders");`,
	} {
		if !strings.Contains(string(messages), want) {
			t.Errorf("Messages.cs missing %q:\n%s", want, messages)
		}
	}

	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`builder.AddExtractor("order-sweep")`,
		`.From(Ports.OrderSweepSource)`,
		`.Publishes(Messages.Orders)`,
		`builder.AddLoader("order-file-loader")`,
		`.Subscribes(Messages.Orders)`,
		`.To(Ports.OrderFileLoaderDestination)`,
	} {
		if !strings.Contains(string(system), want) {
			t.Errorf("OrderFlowSystem.cs missing %q:\n%s", want, system)
		}
	}

	csproj, err := os.ReadFile(filepath.Join(outDir, "OrderFlow.SystemHost.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(csproj), `<ProjectReference Include="../Contracts/Contracts.csproj"`) {
		t.Errorf("csproj missing the contracts reference:\n%s", csproj)
	}
	if !strings.Contains(string(csproj), `Intropy.Topology.Aspire" Version=`) {
		t.Errorf("csproj missing the topology pin:\n%s", csproj)
	}
}

// A transactional-integration-only system renders ports and skip files, and
// no contracts reference: nothing to type.
func TestRealSystemHostTransactionalOnly(t *testing.T) {
	outDir, _ := renderRealHost(t, map[string]any{
		"name":       "trans",
		"topics":     []any{},
		"messages":   []any{},
		"ports":      []any{map[string]any{"name": "erp-source"}, map[string]any{"name": "erp-destination"}},
		"components": []any{map[string]any{"appId": "erp-sync", "kind": "transactional-integration", "fromPort": "erp-source", "toPort": "erp-destination"}},
	})
	if _, err := os.Stat(filepath.Join(outDir, "Messages.cs")); !os.IsNotExist(err) {
		t.Errorf("Messages.cs should not render, err = %v", err)
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

// Field collisions surface as render failures naming both messages and the
// derived field — the message-first counterpart of the old Topics.cs check.
func TestRealSystemHostMessageFieldCollisionFails(t *testing.T) {
	tmpl := loadRealHostTemplate(t)
	values, err := template.Resolve(tmpl, nil, nil, map[string]any{
		"name": "collide",
		"topics": []any{
			map[string]any{"pubsub": msgPub, "name": "order-events", "contract": msgContract},
			map[string]any{"pubsub": msgPub, "name": "order.events", "contract": msgContract},
		},
		"messages": []any{
			msgEntry("order-events", msgContract, "a"),
			msgEntry("order.events", msgContract, "b"),
		},
		"ports":      []any{},
		"components": []any{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = template.RenderFiltered(filepath.Join(templatesDir(t), "system-host", "skeleton"), t.TempDir(), values, tmpl.Spec.Files)
	if err == nil || !strings.Contains(err.Error(), "both derive the Messages.cs field OrderEvents") {
		t.Fatalf("err = %v, want a field-collision failure naming both messages", err)
	}
}

// A pre-scalar payload — components carrying legacy topic wiring instead of
// the scalar message keys — fails the render loudly. Old workspaces migrate
// by re-scaffolding; the render never guesses a message view from topics.
func TestRealSystemHostTopicOnlyPayloadFailsLoudly(t *testing.T) {
	tmpl := loadRealHostTemplate(t)
	values, err := template.Resolve(tmpl, nil, nil, map[string]any{
		"name": "order-flow",
		"topics": []any{
			map[string]any{"pubsub": msgPub, "name": msgName, "contract": msgContract},
		},
		"messages": []any{
			msgEntry(msgName, msgContract, "order-extractor"),
		},
		"ports": []any{
			map[string]any{"name": "order-extractor-source"},
			map[string]any{"name": "order-loader-destination"},
		},
		"components": []any{
			map[string]any{"appId": "order-extractor", "kind": "extractor", "topic": map[string]any{"pubsub": msgPub, "name": msgName}, "port": "order-extractor-source"},
			map[string]any{"appId": "order-loader", "kind": "loader", "topic": map[string]any{"pubsub": msgPub, "name": msgName}, "port": "order-loader-destination"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = template.RenderFiltered(filepath.Join(templatesDir(t), "system-host", "skeleton"), t.TempDir(), values, tmpl.Spec.Files)
	if err == nil || !strings.Contains(err.Error(), "declares no publishes message") {
		t.Fatalf("err = %v, want the pre-scalar payload refusal", err)
	}
}

// Components the template has no branch for abort rendering loudly. The
// branch the api-service kind used to render silently-as-loader is gone
// ahead of WS-3.3 (drift item 2); the loud abort is the pinned behavior.
func TestRealSystemHostUnknownKindFails(t *testing.T) {
	tmpl := loadRealHostTemplate(t)
	values, err := template.Resolve(tmpl, nil, nil, map[string]any{
		"name":     "order-flow",
		"topics":   messageFirstPayload()["topics"],
		"messages": messageFirstPayload()["messages"],
		"ports":    []any{},
		"components": []any{
			map[string]any{"appId": "order-api", "kind": "api-service", "publishes": msgName},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = template.RenderFiltered(filepath.Join(templatesDir(t), "system-host", "skeleton"), t.TempDir(), values, tmpl.Spec.Files)
	if err == nil || !strings.Contains(err.Error(), `has kind "api-service"`) {
		t.Fatalf("err = %v, want a loud abort naming the unknown kind", err)
	}
}

// A component whose message has no entry in the payload's messages section —
// and therefore no payload-contract type — fails the render: the host cannot
// type a MessageRef<T> from nothing.
func TestRealSystemHostUntypedMessageFails(t *testing.T) {
	tmpl := loadRealHostTemplate(t)
	values, err := template.Resolve(tmpl, nil, nil, map[string]any{
		"name": "order-flow",
		"topics": []any{
			map[string]any{"pubsub": msgPub, "name": msgName, "contract": msgContract},
		},
		// The message section deliberately omits the wired message, so it
		// carries no contract anywhere in the payload.
		"messages": []any{},
		"ports":    []any{map[string]any{"name": "order-extractor-source"}},
		"components": []any{
			payloadExtractor("order-extractor", "order-extractor-source"),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = template.RenderFiltered(filepath.Join(templatesDir(t), "system-host", "skeleton"), t.TempDir(), values, tmpl.Spec.Files)
	if err == nil || !strings.Contains(err.Error(), "carries no contract for it") {
		t.Fatalf("err = %v, want the untyped-message contract failure", err)
	}
}

// The shared-contracts dependency keys off the messages section now, and its
// contract value comes from the first contract-bearing message.
func TestRealSystemHostSharedContractsWhen(t *testing.T) {
	tmpl := loadRealHostTemplate(t)
	if len(tmpl.Spec.Dependencies) != 1 {
		t.Fatalf("dependencies = %+v", tmpl.Spec.Dependencies)
	}
	dep := tmpl.Spec.Dependencies[0]
	if dep.Template != "shared-contracts" || dep.When == "" {
		t.Fatalf("dependency = %+v", dep)
	}

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
	contract := func(payload map[string]any) string {
		values, err := template.Resolve(tmpl, nil, nil, payload, nil)
		if err != nil {
			t.Fatal(err)
		}
		tt, err := texttemplate.New("c").Funcs(sprig.TxtFuncMap()).Option("missingkey=error").Parse(dep.Values["contract"])
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		if err := tt.Execute(&sb, values); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}

	msgPayload := messageFirstPayload()
	if got := eval(msgPayload); got == "false" || got == "" {
		t.Errorf("message-bearing system without contracts should render the dependency, when = %q", got)
	}
	if got := contract(msgPayload); got != msgContract {
		t.Errorf("dependency contract = %q, want %q", got, msgContract)
	}
	withContracts := messageFirstPayload()
	withContracts["sharedContracts"] = map[string]any{"name": "Contracts", "include": "../Contracts/Contracts.csproj"}
	if got := eval(withContracts); got != "false" {
		t.Errorf("existing contracts should skip the dependency, when = %q", got)
	}
	noMessages := map[string]any{"name": "trans", "topics": []any{}, "messages": []any{}, "ports": []any{}, "components": []any{}}
	if got := eval(noMessages); got != "false" {
		t.Errorf("message-free system should skip the dependency, when = %q", got)
	}
	if _, err := template.LoadTemplate(filepath.Join(templatesDir(t), dep.Template, "template.yaml")); err != nil {
		t.Fatalf("dependency template: %v", err)
	}
}

// The update flow against the real template. The baseline is a message-first
// host record rendered by this same checkout; the orphan is a scalar loader
// whose subscription resolves to the declared message.
func TestRealSystemUpdateFoldsBlockOrphan(t *testing.T) {
	src, version := realTemplatesSource(t)
	tmpl := loadRealHostTemplate(t)

	// Baseline: extractor-only message-first record, fully rendered — the
	// state sys create leaves behind.
	base := messageFirstPayload()
	base["components"] = []any{payloadExtractor("order-sweep", "order-sweep-source")}
	base["ports"] = []any{map[string]any{"name": "order-sweep-source"}}
	baseValues, err := template.Resolve(tmpl, nil, nil, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	hostDir := filepath.Join(ws, "order-flow")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	renderResolvedRealHost(t, tmpl, baseValues, hostDir)
	if err := template.WriteScaffold(hostDir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "system-host",
		Owner:         "o",
		Repo:          "r",
		Version:       version,
		Role:          template.RoleSystemHost,
		BlockKind:     "",
		Values:        baseValues,
	}); err != nil {
		t.Fatal(err)
	}
	// The declaring sibling: the extractor's own scaffold record with the
	// scalar publish wiring.
	writeRecord(t, filepath.Join(ws, "order-sweep"), template.Scaffold{
		Template: "extractor",
		Owner:    "o", Repo: "r", Version: version,
		BlockKind: template.BlockKindExtractor,
		DataFlow:  "in",
		Values: map[string]any{
			"appId": "order-sweep", "name": "OrderSweep", "organization": "Maxbo",
			"payloadType": msgContract, "channel": msgName,
			"port": "order-sweep-source", "projectName": "OrderSweep",
			template.KeyPublishes: msgName,
		},
	})

	// The orphan: a loader scaffolded after the host was created, subscribing
	// the already-declared message.
	orphanDir := filepath.Join(ws, "order-file-loader")
	writeRecord(t, orphanDir, template.Scaffold{
		Template: "loader",
		Owner:    "o", Repo: "r", Version: version,
		BlockKind: template.BlockKindLoader,
		DataFlow:  "out",
		Values: map[string]any{
			"appId": "order-file-loader", "name": "OrderFileLoader", "organization": "Maxbo",
			"payloadType": msgContract, "channel": msgName,
			"port": "order-file-loader-destination", "projectName": "OrderFileLoader",
			template.KeySubscribes: msgName,
		},
	})

	var stderr bytes.Buffer
	err = Update(t.Context(), UpdateOptions{StartDir: ws, Stderr: &stderr, Stdout: io.Discard, Source: src, Version: version})
	if err != nil {
		t.Fatalf("Update: %v\nstderr: %s", err, stderr.String())
	}
	for _, want := range []string{"updating " + hostDir, "added 1 component(s)"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}

	system, err := os.ReadFile(filepath.Join(hostDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`.Publishes(Messages.Orders)`,
		`builder.AddLoader("order-file-loader")`,
		`.Subscribes(Messages.Orders)`,
		`.To(Ports.OrderFileLoaderDestination)`,
	} {
		if !strings.Contains(string(system), want) {
			t.Errorf("OrderFlowSystem.cs missing %q:\n%s", want, system)
		}
	}

	rec, err := template.LoadScaffold(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	components := rec.Values["components"].([]any)
	if len(components) != 2 {
		t.Fatalf("record declares %d components, want 2", len(components))
	}
	topics := rec.Values["topics"].([]any)
	if len(topics) != 1 {
		t.Errorf("record declares %d topics, want 1 — the orphan's channel is the baseline's", len(topics))
	}
	messages := rec.Values["messages"].([]any)
	if len(messages) != 1 {
		t.Errorf("record declares %d messages, want 1 — an orphan naming a declared message must not duplicate it", len(messages))
	}
	if got := jsonOf(t, topics); got != jsonOf(t, baseValues["topics"]) {
		t.Errorf("baseline topics changed through the update:\n%s\nwant\n%s", got, jsonOf(t, baseValues["topics"]))
	}

	// Idempotence: a second run is a no-op.
	stderr.Reset()
	err = Update(t.Context(), UpdateOptions{StartDir: ws, Stderr: &stderr, Stdout: io.Discard, Source: src, Version: version})
	if err != nil {
		t.Fatalf("second Update: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no orphaned components found") {
		t.Errorf("second run stderr = %q, want the empty state", stderr.String())
	}
}

// The honest-baseline contract holds against the real template: a render
// conflict refuses the update, the record stays un-rewritten, and a clean
// re-run retries successfully.
func TestRealSystemUpdateConflictKeepsBaseline(t *testing.T) {
	src, version := realTemplatesSource(t)
	tmpl := loadRealHostTemplate(t)

	base := messageFirstPayload()
	base["components"] = []any{payloadExtractor("order-sweep", "order-sweep-source")}
	base["ports"] = []any{map[string]any{"name": "order-sweep-source"}}
	baseValues, err := template.Resolve(tmpl, nil, nil, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	hostDir := filepath.Join(ws, "order-flow")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	renderResolvedRealHost(t, tmpl, baseValues, hostDir)
	if err := template.WriteScaffold(hostDir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "system-host",
		Owner:         "o", Repo: "r", Version: version,
		Role:   template.RoleSystemHost,
		Values: baseValues,
	}); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, filepath.Join(ws, "order-sweep"), template.Scaffold{
		Template: "extractor",
		Owner:    "o", Repo: "r", Version: version,
		BlockKind: template.BlockKindExtractor,
		DataFlow:  "in",
		Values: map[string]any{
			"appId": "order-sweep", "name": "OrderSweep", "organization": "Maxbo",
			"payloadType": msgContract, "channel": msgName,
			"port": "order-sweep-source", "projectName": "OrderSweep",
			template.KeyPublishes: msgName,
		},
	})
	orphanDir := filepath.Join(ws, "order-file-loader")
	writeRecord(t, orphanDir, template.Scaffold{
		Template: "loader",
		Owner:    "o", Repo: "r", Version: version,
		BlockKind: template.BlockKindLoader,
		DataFlow:  "out",
		Values: map[string]any{
			"appId": "order-file-loader", "name": "OrderFileLoader", "organization": "Maxbo",
			"payloadType": msgContract, "channel": msgName,
			"port": "order-file-loader-destination", "projectName": "OrderFileLoader",
			template.KeySubscribes: msgName,
		},
	})

	conflicted := filepath.Join(hostDir, "Ports.cs")
	portsBefore, err := os.ReadFile(conflicted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflicted, []byte("// hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recordBefore, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	err = Update(t.Context(), UpdateOptions{StartDir: ws, Stderr: &stderr, Stdout: io.Discard, Source: src, Version: version})
	if err == nil || !strings.Contains(err.Error(), "Ports.cs") {
		t.Fatalf("err = %v, want a conflict naming Ports.cs\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error does not name the escape hatch: %v", err)
	}
	recordAfter, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordBefore, recordAfter) {
		t.Error("conflicted update rewrote the scaffold record — a rerun would miss the orphan")
	}

	// A clean re-run retries the whole folded update.
	if err := os.WriteFile(conflicted, portsBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if err := Update(t.Context(), UpdateOptions{StartDir: ws, Stderr: &stderr, Stdout: io.Discard, Source: src, Version: version}); err != nil {
		t.Fatalf("re-run: %v\nstderr: %s", err, stderr.String())
	}
	system, err := os.ReadFile(filepath.Join(hostDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(system), `builder.AddLoader("order-file-loader")`) {
		t.Errorf("re-run did not fold the orphan:\n%s", system)
	}
}

// A legacy pre-scalar baseline moves onto the message-first template the
// documented way: assembly refuses the record with the migration error and
// the record stays un-rewritten — the honest-baseline contract means the
// next run (after the workspace is migrated) starts from the same state.
func TestRealSystemUpdateLegacyBaselineFailsLoudly(t *testing.T) {
	src, version := realTemplatesSource(t)

	ws := t.TempDir()
	hostDir := filepath.Join(ws, "order-flow")
	legacyValues := map[string]any{
		"name":        "order-flow",
		"projectName": "OrderFlow",
		"systemClass": "OrderFlowSystem",
		"topics":      []any{map[string]any{"pubsub": msgPub, "name": msgName, "contract": msgContract}},
		"ports":       []any{map[string]any{"name": "order-extractor-source"}},
		"components": []any{
			map[string]any{"appId": "order-extractor", "kind": "extractor", "topic": map[string]any{"pubsub": msgPub, "name": msgName}, "port": "order-extractor-source"},
		},
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := template.WriteScaffold(hostDir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "system-host",
		Owner:         "o", Repo: "r", Version: version,
		Role:   template.RoleSystemHost,
		Values: legacyValues,
	}); err != nil {
		t.Fatal(err)
	}
	// A legacy extractor sibling: topic wiring, no scalar publishes value.
	writeRecord(t, filepath.Join(ws, "order-extractor"), template.Scaffold{
		Template: "extractor",
		Owner:    "o", Repo: "r", Version: version,
		BlockKind: template.BlockKindExtractor,
		DataFlow:  "in",
		Values: map[string]any{
			"appId": "order-extractor", "name": "OrderExtractor", "organization": "Maxbo",
			"payloadType": msgContract, "channel": msgName,
			"port": "order-extractor-source", "projectName": "OrderExtractor",
			"message": msgName, "topic": msgName, "pubsub": msgPub, "contract": msgContract,
		},
	})

	recordBefore, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	err = Update(t.Context(), UpdateOptions{StartDir: ws, Stderr: &stderr, Stdout: io.Discard, Source: src, Version: version})
	if err == nil || !strings.Contains(err.Error(), "predates the scalar message DSL") {
		t.Fatalf("err = %v, want the pre-scalar migration refusal\nstderr: %s", err, stderr.String())
	}
	recordAfter, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordBefore, recordAfter) {
		t.Error("failed update rewrote the scaffold record")
	}
}

// renderResolvedRealHost renders an already-resolved value map into dst and
// copies the files in — the state a completed sys create leaves on disk.
func renderResolvedRealHost(t *testing.T, tmpl *template.Template, values map[string]any, dst string) {
	t.Helper()
	tmp := t.TempDir()
	if err := template.RenderFiltered(filepath.Join(templatesDir(t), "system-host", "skeleton"), tmp, values, tmpl.Spec.Files); err != nil {
		t.Fatalf("render baseline: %v", err)
	}
	if err := os.CopyFS(dst, os.DirFS(tmp)); err != nil {
		t.Fatal(err)
	}
}

// writeRecord writes a sibling scaffold record the way `int create` leaves
// one.
func writeRecord(t *testing.T, dir string, s template.Scaffold) {
	t.Helper()
	if err := template.WriteScaffold(dir, s); err != nil {
		t.Fatal(err)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// realTemplatesSource mirrors the helper in internal/template: mirror the
// checkout's current branch into a throwaway repo and pin a tag-shaped name
// (a slash in the pin would collide with the cache's temp-dir naming).
func realTemplatesSource(t *testing.T) (source template.SourceOptions, pin string) {
	const tag = "message-first-e2e"
	t.Helper()
	dir := templatesDir(t)
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", abs, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("read the checkout's branch: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		t.Fatalf("INTROPY_TEMPLATES_DIR %s is on a detached HEAD; point it at a branch", abs)
	}
	mirrorRoot := filepath.Join(t.TempDir(), "templates-mirror")
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", branch, abs, mirrorRoot).CombinedOutput(); err != nil {
		t.Fatalf("mirror the checkout at %s: %v\n%s", branch, err, out)
	}
	if out, err := exec.Command("git", "-C", mirrorRoot, "tag", tag, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("pin the mirrored checkout: %v\n%s", err, out)
	}
	return template.SourceOptions{RepoURL: "file://" + mirrorRoot, CacheRoot: t.TempDir()}, tag
}
