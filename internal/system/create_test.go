package system

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// systemHostTemplateYAML is a trimmed but faithful copy of the real
// system-host template manifest: same required parameter, same pattern,
// same spec.values derivations.
const systemHostTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: system-host
  title: System Host
  labels:
    intropy.dev/template-role: system-host
spec:
  parameters:
    type: object
    required: [name]
    properties:
      name:
        type: string
        pattern: "^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$"
  values:
    projectName: '{{ .name | replace "-" " " | title | replace " " "" }}'
    systemClass: '{{ .name | replace "-" " " | title | replace " " "" }}System'
    pubsub: 'pubsub'
`

const systemHostCsprojTmpl = `<Project Sdk="Microsoft.NET.Sdk">

    <PropertyGroup>
        <OutputType>Exe</OutputType>
        <RootNamespace>{{ .projectName }}.SystemHost</RootNamespace>
    </PropertyGroup>

</Project>
`

func systemHostFiles() map[string]string {
	return map[string]string{
		"system-host/template.yaml":                                      systemHostTemplateYAML,
		"system-host/skeleton/Program.cs":                                "// dispatch\n",
		"system-host/skeleton/Topics.cs.tmpl":                            "// placeholder {{ .pubsub }}\n",
		"system-host/skeleton/Connectors.cs.tmpl":                        "// placeholder\n",
		"system-host/skeleton/{{ .systemClass }}.cs.tmpl":                "// placeholder {{ .systemClass }}\n",
		"system-host/skeleton/{{ .projectName }}.SystemHost.csproj.tmpl": systemHostCsprojTmpl,
	}
}

func buildTarGz(t *testing.T, prefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		full := name
		if prefix != "" {
			full = prefix + "/" + name
		}
		h := &tar.Header{Name: full, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newSystemHostServer serves the system-host fixture tarball and counts
// every request so tests can assert validation happens before network I/O.
func newSystemHostServer(t *testing.T, tag string, files map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	tarball := buildTarGz(t, "owner-repo-abc123", files)
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/repos/o/r/tarball/"+tag, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(tarball)
	})
	return httptest.NewServer(mux), &hits
}

// writeWorkspace lays out the tutorial's order-flow workspace: an
// extractor, a loader, and the shared Contracts project, each with the
// scaffold record int create would leave.
func writeWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	writeBlock(t, filepath.Join(ws, "order-extractor"), template.BlockKindExtractor, "order-extractor")
	writeBlock(t, filepath.Join(ws, "order-loader"), template.BlockKindLoader, "order-loader")
	writeShared(t, filepath.Join(ws, "Contracts"), "Contracts")
	return ws
}

func writeBlock(t *testing.T, dir, kind, appID string) {
	t.Helper()
	connector := appID + "-source"
	if kind == template.BlockKindLoader {
		connector = appID + "-destination"
	}
	err := template.WriteScaffold(dir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      kind,
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		BlockKind:     kind,
		Values: map[string]any{
			"appId": appID, "topic": "orders", "contract": "Order", "pubsub": "pubsub", "connector": connector,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeShared(t *testing.T, dir, name string) {
	t.Helper()
	err := template.WriteScaffold(dir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "shared-contracts",
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		Role:          template.RoleSharedLibrary,
		Values:        map[string]any{"name": name},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runCreate(t *testing.T, srv *httptest.Server, opts CreateOptions) (stdout, stderr bytes.Buffer, err error) {
	t.Helper()
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	opts.HTTP = srv.Client()
	opts.Owner = "o"
	opts.Repo = "r"
	opts.GitHubBaseURL = srv.URL
	err = Create(context.Background(), opts)
	return stdout, stderr, err
}

const wantTopicsCS = `using Intropy.Topology;
using Contracts;

/// <summary>The system's topics, each defined once and shared by every component that touches it.</summary>
public static class Topics
{
    /// <summary>Order messages on topic 'orders' (pubsub 'pubsub').</summary>
    public static readonly TopicRef<Order> Orders = TopicRef<Order>.Define("pubsub", "orders");
}
`

const wantSystemCS = `using Intropy.Topology;

/// <summary>The order-flow system: what exists, and what connects it.</summary>
public sealed class OrderFlowSystem : ISystemDefinition
{
    public string SystemName => "order-flow";

    public void Define(SystemBuilder builder)
    {
        builder.AddExtractor("order-extractor")
            .From(Connectors.OrderExtractorSource)
            .Publishes(Topics.Orders)
            .WithSchedule("* * * * *");
        builder.AddLoader("order-loader")
            .Subscribes(Topics.Orders)
            .To(Connectors.OrderLoaderDestination);
    }
}
`

const wantConnectorsCS = `using Intropy.Topology;

/// <summary>The system's connectors: the named ports its edge blocks reach the outside world through. Each defaults to a local file folder under test/ so the system runs with zero external configuration.</summary>
public static class Connectors
{
    /// <summary>Local file connector 'order-extractor-source' (folder ./test/order-extractor-source). Point it at a real external system and transport when known.</summary>
    public static readonly ConnectorRef OrderExtractorSource = ConnectorRef.Define("order-extractor-source", Transport.File("./test/order-extractor-source"));

    /// <summary>Local file connector 'order-loader-destination' (folder ./test/order-loader-destination). Point it at a real external system and transport when known.</summary>
    public static readonly ConnectorRef OrderLoaderDestination = ConnectorRef.Define("order-loader-destination", Transport.File("./test/order-loader-destination"));
}
`

// wantLegacySystemCS is the pre-connector shape: a template release without
// the Connectors.cs placeholder degrades the system class to no From/To.
const wantLegacySystemCS = `using Intropy.Topology;

/// <summary>The order-flow system: what exists, and what connects it.</summary>
public sealed class OrderFlowSystem : ISystemDefinition
{
    public string SystemName => "order-flow";

    public void Define(SystemBuilder builder)
    {
        builder.AddExtractor("order-extractor")
            .Publishes(Topics.Orders);
        builder.AddLoader("order-loader")
            .Subscribes(Topics.Orders);
    }
}
`

func TestCreateAssemblesSystem(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	ws := writeWorkspace(t)
	outDir := filepath.Join(ws, "system-host")
	stdout, stderr, err := runCreate(t, srv, CreateOptions{
		Name:       "OrderFlow",
		StartDir:   ws,
		OutputDir:  outDir,
		Version:    "v1",
		OutputJSON: "-",
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	topics, err := os.ReadFile(filepath.Join(outDir, "Topics.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(topics) != wantTopicsCS {
		t.Errorf("Topics.cs:\n%s\nwant:\n%s", topics, wantTopicsCS)
	}

	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(system) != wantSystemCS {
		t.Errorf("OrderFlowSystem.cs:\n%s\nwant:\n%s", system, wantSystemCS)
	}

	connectors, err := os.ReadFile(filepath.Join(outDir, "Connectors.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(connectors) != wantConnectorsCS {
		t.Errorf("Connectors.cs:\n%s\nwant:\n%s", connectors, wantConnectorsCS)
	}

	for _, folder := range []string{"order-extractor-source", "order-loader-destination"} {
		if fi, err := os.Stat(filepath.Join(outDir, "test", folder)); err != nil || !fi.IsDir() {
			t.Errorf("expected connector test folder test/%s: %v", folder, err)
		}
	}

	csproj, err := os.ReadFile(filepath.Join(outDir, "OrderFlow.SystemHost.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	ref := `<ProjectReference Include="../Contracts/Contracts.csproj" IsAspireProjectResource="false" />`
	refIdx := strings.Index(string(csproj), ref)
	endIdx := strings.Index(string(csproj), "</Project>")
	if refIdx < 0 || endIdx < refIdx {
		t.Errorf("csproj missing shared contracts reference before </Project>:\n%s", csproj)
	}

	record, err := template.LoadScaffold(filepath.Join(outDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("host scaffold record: %v", err)
	}
	if record.Role != template.RoleSystemHost || record.Values["systemClass"] != "OrderFlowSystem" {
		t.Errorf("host record = %+v", record)
	}

	var result CreateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output JSON: %v\n%s", err, stdout.String())
	}
	if result.System.Name != "order-flow" || len(result.System.Components) != 2 || len(result.System.Topics) != 1 {
		t.Errorf("result.System = %+v", result.System)
	}
	if len(result.System.Connectors) != 2 || result.System.Connectors[0].Name != "order-extractor-source" {
		t.Errorf("result.System.Connectors = %+v", result.System.Connectors)
	}
	if !strings.Contains(stderr.String(), `assembled system "order-flow": 2 component(s), 1 topic(s), 2 connector(s)`) {
		t.Errorf("stderr = %s", stderr.String())
	}
}

func TestCreateWithoutConnectorsPlaceholderDegradesToLegacyOutput(t *testing.T) {
	files := systemHostFiles()
	delete(files, "system-host/skeleton/Connectors.cs.tmpl")
	srv, _ := newSystemHostServer(t, "v1", files)
	defer srv.Close()

	ws := writeWorkspace(t)
	outDir := filepath.Join(ws, "system-host")
	_, stderr, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1"})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(outDir, "Connectors.cs")); !os.IsNotExist(err) {
		t.Errorf("Connectors.cs should not be created without the placeholder; stat err = %v", err)
	}
	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(system) != wantLegacySystemCS {
		t.Errorf("OrderFlowSystem.cs:\n%s\nwant legacy shape:\n%s", system, wantLegacySystemCS)
	}
	if _, err := os.Stat(filepath.Join(outDir, "test")); !os.IsNotExist(err) {
		t.Errorf("test/ folders should not be created without connectors; stat err = %v", err)
	}
	if !strings.Contains(stderr.String(), "no Connectors.cs placeholder") {
		t.Errorf("stderr should warn about the missing placeholder:\n%s", stderr.String())
	}
}

func TestCreateWithRecordMissingConnectorOmitsItsFromTo(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	// The loader's record predates the connector value; the extractor's has it.
	ws := t.TempDir()
	writeBlock(t, filepath.Join(ws, "order-extractor"), template.BlockKindExtractor, "order-extractor")
	writeShared(t, filepath.Join(ws, "Contracts"), "Contracts")
	err := template.WriteScaffold(filepath.Join(ws, "order-loader"), template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      template.BlockKindLoader,
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		BlockKind:     template.BlockKindLoader,
		Values: map[string]any{
			"appId": "order-loader", "topic": "orders", "contract": "Order", "pubsub": "pubsub",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(ws, "system-host")
	_, stderr, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1"})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(system), ".From(Connectors.OrderExtractorSource)") {
		t.Errorf("extractor should keep its From:\n%s", system)
	}
	if !strings.Contains(string(system), ".Subscribes(Topics.Orders);") {
		t.Errorf("loader without a connector should have no To:\n%s", system)
	}
	if !strings.Contains(stderr.String(), "has no connector") {
		t.Errorf("stderr should warn about the missing connector:\n%s", stderr.String())
	}
}

func TestCreateDefaultsOutputDirToKebabName(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	ws := writeWorkspace(t)
	t.Chdir(ws)
	_, stderr, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", Version: "v1"})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "order-flow", "Topics.cs")); err != nil {
		t.Errorf("expected output in ./order-flow: %v", err)
	}
}

func TestCreateRefusesNonEmptyOutputDir(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	ws := writeWorkspace(t)
	outDir := filepath.Join(ws, "system-host")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1"})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want --force guidance", err)
	}
}

func TestCreateForceRerunSkipsOldHost(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	ws := writeWorkspace(t)
	outDir := filepath.Join(ws, "system-host")
	if _, stderr, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1"}); err != nil {
		t.Fatalf("first Create: %v\nstderr: %s", err, stderr.String())
	}

	_, stderr, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1", Force: true})
	if err != nil {
		t.Fatalf("re-run with --force: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "an existing system host") {
		t.Errorf("stderr should warn about the old host record:\n%s", stderr.String())
	}
}

func TestCreateValidatesBeforeNetwork(t *testing.T) {
	srv, hits := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	ws := t.TempDir()
	dir := filepath.Join(ws, "order-extractor")
	err := template.WriteScaffold(dir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "extractor",
		BlockKind:     template.BlockKindExtractor,
		Values:        map[string]any{"appId": "order-extractor", "topic": "orders"}, // no contract
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, Version: "v1"})
	if err == nil || !strings.Contains(err.Error(), "values.contract is missing") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("validation errors must surface before any GitHub request; got %d requests", hits.Load())
	}
}

func TestCreateFailsWhenPlaceholderMissing(t *testing.T) {
	files := systemHostFiles()
	delete(files, "system-host/skeleton/{{ .systemClass }}.cs.tmpl")
	files["system-host/skeleton/Other.cs"] = "// not the placeholder\n"
	srv, _ := newSystemHostServer(t, "v1", files)
	defer srv.Close()

	ws := writeWorkspace(t)
	_, _, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: filepath.Join(ws, "system-host"), Version: "v1"})
	if err == nil || !strings.Contains(err.Error(), "rendered no OrderFlowSystem.cs placeholder") {
		t.Errorf("err = %v, want placeholder gate", err)
	}
}
