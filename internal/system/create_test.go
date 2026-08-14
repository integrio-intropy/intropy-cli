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

// systemHostTemplateYAML is a trimmed but faithful copy of the
// facts-payload system-host manifest: the payload lists are required (an old
// CLI that only sets `name` fails validation), sharedContracts is optional
// (a topic-free system has none), Topics.cs renders only when topics exist,
// and the host scaffolds the contracts project itself for a topic-bearing
// system whose workspace lacks one. The projectName/systemClass derivations
// match the real template.
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
    required: [name, topics, ports, components]
    properties:
      name:
        type: string
        pattern: "^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$"
      topics:
        type: array
        items:
          type: object
          additionalProperties: true
      ports:
        type: array
        items:
          type: object
          additionalProperties: true
      components:
        type: array
        items:
          type: object
          additionalProperties: true
      sharedContracts:
        type: object
        additionalProperties: true
  files:
    - path: Topics.cs.tmpl
      when: '{{ gt (len .topics) 0 }}'
  dependencies:
    - template: shared-contracts
      when: '{{ and (gt (len .topics) 0) (not (hasKey . "sharedContracts")) }}'
      output: Contracts
      values:
        name: Contracts
  values:
    projectName: '{{ .name | replace "-" " " | title | replace " " "" }}'
    systemClass: '{{ .name | replace "-" " " | title | replace " " "" }}System'
`

// sharedContractsTemplateYAML is the dependency the host scaffolds for a
// topic-bearing system without a contracts sibling. Trimmed like the host
// fixture: one file, one required parameter.
const sharedContractsTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: shared-contracts
  title: Shared Contracts
  labels:
    intropy.dev/template-role: shared-library
spec:
  parameters:
    type: object
    required: [name]
    properties:
      name:
        type: string
`

// systemHostTemplateLegacyYAML is the pre-facts manifest shape: requiring
// sharedContracts marks a release that consumes the CLI-joined payload this
// CLI no longer produces.
const systemHostTemplateLegacyYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: system-host
  title: System Host
  labels:
    intropy.dev/template-role: system-host
spec:
  parameters:
    type: object
    required: [name, topics, ports, components, sharedContracts]
    properties:
      name:
        type: string
      topics:
        type: array
      ports:
        type: array
      components:
        type: array
      sharedContracts:
        type: object
  values:
    projectName: '{{ .name | replace "-" " " | title | replace " " "" }}'
    systemClass: '{{ .name | replace "-" " " | title | replace " " "" }}System'
`

// The fixture declaration templates mirror the shapes the template library
// renders from the facts-only payload: field identifiers, the joins from
// components to those fields, and the csproj reference path are all derived
// here — the CLI supplies names and wiring only. The assertions below check
// the CLI assembled the payload correctly — the template repo owns the exact
// C# text.

const topicsCSTmpl = `using Intropy.Topology;
{{- if hasKey . "sharedContracts" }}
using {{ .sharedContracts.name }};
{{- end }}

public static class Topics
{
{{- range $t := .topics }}
    public static readonly TopicRef<{{ $t.contract }}> {{ regexReplaceAll "[-._]" $t.name " " | title | replace " " "" }} = TopicRef<{{ $t.contract }}>.Define("{{ $t.pubsub }}", "{{ $t.name }}");
{{- end }}
}
`

const portsCSTmpl = `using Intropy.Topology;

public static class Ports
{
{{- range $c := .ports }}
    public static readonly PortRef {{ regexReplaceAll "[-._]" $c.name " " | title | replace " " "" }} = PortRef.Define("{{ $c.name }}");
{{- end }}
}
`

const developmentCSTmpl = `public sealed class {{ .projectName }}Development : IDevelopmentDefinition
{
    public void Define(DevelopmentBuilder development)
    {
{{- range $c := .ports }}
        development.Files(Ports.{{ regexReplaceAll "[-._]" $c.name " " | title | replace " " "" }}).RootPath("./test/{{ $c.name }}");
{{- end }}
    }
}
`

// The system class joins each component's wiring names to the fields Topics
// and Ports declare, keyed by the raw names so the derivation matches
// the declaration templates exactly.
const systemClassCSTmpl = `{{- $topicField := dict -}}
{{- range $t := .topics -}}
  {{- $key := printf "%s/%s" $t.pubsub $t.name -}}
  {{- $_ := set $topicField $key (regexReplaceAll "[-._]" $t.name " " | title | replace " " "") -}}
{{- end -}}
{{- $portField := dict -}}
{{- range $c := .ports -}}
  {{- $_ := set $portField $c.name (regexReplaceAll "[-._]" $c.name " " | title | replace " " "") -}}
{{- end -}}
public sealed class {{ .systemClass }} : ISystemDefinition
{
    public string SystemName => "{{ .name }}";

    public void Define(SystemBuilder builder)
    {
{{- range .components }}
{{- if eq .kind "extractor" }}
        builder.AddExtractor("{{ .appId }}")
{{- if hasKey . "port" }}
            .From(Ports.{{ index $portField .port }})
{{- end }}
            .Publishes(Topics.{{ index $topicField (printf "%s/%s" .topic.pubsub .topic.name) }});
{{- else if eq .kind "transactional-integration" }}
        builder.AddTransactionalIntegration("{{ .appId }}")
            .From(Ports.{{ index $portField .from }})
            .To(Ports.{{ index $portField .to }});
{{- else }}
        builder.AddLoader("{{ .appId }}")
            .Subscribes(Topics.{{ index $topicField (printf "%s/%s" .topic.pubsub .topic.name) }})
{{- if hasKey . "port" }}
            .To(Ports.{{ index $portField .port }})
{{- end }};
{{- end }}
{{- end }}
    }
}
`

const systemHostCsprojTmpl = `<Project Sdk="Microsoft.NET.Sdk">

    <PropertyGroup>
        <OutputType>Exe</OutputType>
        <RootNamespace>{{ .projectName }}.SystemHost</RootNamespace>
    </PropertyGroup>

{{- if hasKey . "sharedContracts" }}
    <ItemGroup>
        <ProjectReference Include="{{ .sharedContracts.include }}" IsAspireProjectResource="false" />
    </ItemGroup>
{{- end }}

</Project>
`

func systemHostFiles() map[string]string {
	return map[string]string{
		"system-host/template.yaml":                                      systemHostTemplateYAML,
		"system-host/skeleton/Program.cs":                                "// dispatch\n",
		"system-host/skeleton/Topics.cs.tmpl":                            topicsCSTmpl,
		"system-host/skeleton/Ports.cs.tmpl":                             portsCSTmpl,
		"system-host/skeleton/{{ .projectName }}Development.cs.tmpl":     developmentCSTmpl,
		"system-host/skeleton/{{ .systemClass }}.cs.tmpl":                systemClassCSTmpl,
		"system-host/skeleton/{{ .projectName }}.SystemHost.csproj.tmpl": systemHostCsprojTmpl,
		"shared-contracts/template.yaml":                                 sharedContractsTemplateYAML,
		"shared-contracts/skeleton/{{ .name }}.csproj":                   "<Project Sdk=\"Microsoft.NET.Sdk\" />\n",
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
	port := appID + "-source"
	if kind == template.BlockKindLoader {
		port = appID + "-destination"
	}
	err := template.WriteScaffold(dir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      kind,
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		BlockKind:     kind,
		Values: map[string]any{
			"appId": appID, "topic": "orders", "contract": "Order", "pubsub": "pubsub", "port": port,
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
	for _, want := range []string{
		"using Contracts;",
		`TopicRef<Order> Orders = TopicRef<Order>.Define("pubsub", "orders");`,
	} {
		if !strings.Contains(string(topics), want) {
			t.Errorf("Topics.cs missing %q:\n%s", want, topics)
		}
	}

	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`SystemName => "order-flow"`,
		`builder.AddExtractor("order-extractor")`,
		".From(Ports.OrderExtractorSource)",
		".Publishes(Topics.Orders)",
		`builder.AddLoader("order-loader")`,
		".Subscribes(Topics.Orders)",
		".To(Ports.OrderLoaderDestination)",
	} {
		if !strings.Contains(string(system), want) {
			t.Errorf("OrderFlowSystem.cs missing %q:\n%s", want, system)
		}
	}

	ports, err := os.ReadFile(filepath.Join(outDir, "Ports.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`PortRef OrderExtractorSource = PortRef.Define("order-extractor-source");`,
		`PortRef OrderLoaderDestination = PortRef.Define("order-loader-destination");`,
	} {
		if !strings.Contains(string(ports), want) {
			t.Errorf("Ports.cs missing %q:\n%s", want, ports)
		}
	}

	development, err := os.ReadFile(filepath.Join(outDir, "OrderFlowDevelopment.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"class OrderFlowDevelopment",
		`development.Files(Ports.OrderExtractorSource).RootPath("./test/order-extractor-source");`,
		`development.Files(Ports.OrderLoaderDestination).RootPath("./test/order-loader-destination");`,
	} {
		if !strings.Contains(string(development), want) {
			t.Errorf("OrderFlowDevelopment.cs missing %q:\n%s", want, development)
		}
	}

	csproj, err := os.ReadFile(filepath.Join(outDir, "OrderFlow.SystemHost.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	ref := `<ProjectReference Include="../Contracts/Contracts.csproj" IsAspireProjectResource="false" />`
	if !strings.Contains(string(csproj), ref) {
		t.Errorf("csproj missing shared contracts reference:\n%s", csproj)
	}

	record, err := template.LoadScaffold(filepath.Join(outDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("host scaffold record: %v", err)
	}
	if record.Role != template.RoleSystemHost || record.Values["systemClass"] != "OrderFlowSystem" {
		t.Errorf("host record = %+v", record)
	}
	// The record echoes the full payload: it is the honest record of what
	// was rendered.
	if _, ok := record.Values["topics"].([]any); !ok {
		t.Errorf("record values missing topics payload: %+v", record.Values)
	}
	if _, ok := record.Values["sharedContracts"].(map[string]any); !ok {
		t.Errorf("record values missing sharedContracts payload: %+v", record.Values)
	}

	var result CreateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output JSON: %v\n%s", err, stdout.String())
	}
	if result.System.Name != "order-flow" || len(result.System.Components) != 2 || len(result.System.Topics) != 1 {
		t.Errorf("result.System = %+v", result.System)
	}
	if len(result.System.Ports) != 2 || result.System.Ports[0].Name != "order-extractor-source" {
		t.Errorf("result.System.Ports = %+v", result.System.Ports)
	}
	if !strings.Contains(stderr.String(), `assembled system "order-flow": 2 component(s), 1 topic(s), 2 port(s)`) {
		t.Errorf("stderr = %s", stderr.String())
	}
}

func TestCreateWithRecordMissingPortOmitsItsFromTo(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	// The loader's record predates the port value; the extractor's has it.
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
	if !strings.Contains(string(system), ".From(Ports.OrderExtractorSource)") {
		t.Errorf("extractor should keep its From:\n%s", system)
	}
	if strings.Contains(string(system), ".To(") {
		t.Errorf("loader without a port should have no To:\n%s", system)
	}
	if !strings.Contains(stderr.String(), "has no port") {
		t.Errorf("stderr should warn about the missing port:\n%s", stderr.String())
	}
	development, err := os.ReadFile(filepath.Join(outDir, "OrderFlowDevelopment.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(development), `development.Files(Ports.OrderExtractorSource).RootPath("./test/order-extractor-source");`) {
		t.Errorf("development definition should resolve the extractor's port:\n%s", development)
	}
	if strings.Contains(string(development), "order-loader") {
		t.Errorf("development definition should have no resolution for the port-less loader:\n%s", development)
	}
}

// writeTransactional lays down a transactional integration's scaffold
// record: two ports, no topic.
func writeTransactional(t *testing.T, dir, appID, from, to string) {
	t.Helper()
	err := template.WriteScaffold(dir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "transactional",
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		BlockKind:     template.BlockKindTransactional,
		DataFlow:      "both",
		Values: map[string]any{
			"appId": appID, "fromPort": from, "toPort": to,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateAssemblesTransactionalIntegration(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	ws := writeWorkspace(t)
	writeTransactional(t, filepath.Join(ws, "erp-sync"), "erp-sync", "erp-source", "erp-destination")

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

	system, err := os.ReadFile(filepath.Join(outDir, "OrderFlowSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`builder.AddTransactionalIntegration("erp-sync")`,
		".From(Ports.ErpSource)",
		".To(Ports.ErpDestination)",
	} {
		if !strings.Contains(string(system), want) {
			t.Errorf("OrderFlowSystem.cs missing %q:\n%s", want, system)
		}
	}

	ports, err := os.ReadFile(filepath.Join(outDir, "Ports.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`PortRef ErpSource = PortRef.Define("erp-source");`,
		`PortRef ErpDestination = PortRef.Define("erp-destination");`,
	} {
		if !strings.Contains(string(ports), want) {
			t.Errorf("Ports.cs missing %q:\n%s", want, ports)
		}
	}

	development, err := os.ReadFile(filepath.Join(outDir, "OrderFlowDevelopment.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`development.Files(Ports.ErpSource).RootPath("./test/erp-source");`,
		`development.Files(Ports.ErpDestination).RootPath("./test/erp-destination");`,
	} {
		if !strings.Contains(string(development), want) {
			t.Errorf("OrderFlowDevelopment.cs missing %q:\n%s", want, development)
		}
	}

	// The JSON summary: transactional components add `ports` while the
	// topic blocks keep their scalar `port`.
	var result CreateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output JSON: %v\n%s", err, stdout.String())
	}
	if len(result.System.Components) != 3 || len(result.System.Ports) != 4 || len(result.System.Topics) != 1 {
		t.Errorf("result.System = %+v", result.System)
	}
	var tx *Component
	for i := range result.System.Components {
		if result.System.Components[i].Kind == template.BlockKindTransactional {
			tx = &result.System.Components[i]
		}
	}
	if tx == nil {
		t.Fatalf("no transactional component in %+v", result.System.Components)
	}
	if tx.Port != "" || len(tx.Ports) != 2 || tx.Ports[0] != "erp-source" || tx.Ports[1] != "erp-destination" {
		t.Errorf("transactional summary = %+v", *tx)
	}
	if !strings.Contains(stderr.String(), `assembled system "order-flow": 3 component(s), 1 topic(s), 4 port(s)`) {
		t.Errorf("stderr = %s", stderr.String())
	}
}

func TestCreateAssemblesTransactionalOnlySystem(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	// A transactional-only workspace: no topics, no shared-library
	// scaffold — the host renders contracts-free.
	ws := t.TempDir()
	writeTransactional(t, filepath.Join(ws, "erp-sync"), "erp-sync", "erp-source", "erp-destination")

	outDir := filepath.Join(ws, "system-host")
	stdout, stderr, err := runCreate(t, srv, CreateOptions{
		Name:       "Trans",
		StartDir:   ws,
		OutputDir:  outDir,
		Version:    "v1",
		OutputJSON: "-",
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(outDir, "Topics.cs")); !os.IsNotExist(err) {
		t.Errorf("a topic-free system should have no Topics.cs, stat err = %v", err)
	}
	csproj, err := os.ReadFile(filepath.Join(outDir, "Trans.SystemHost.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(csproj), "ProjectReference") {
		t.Errorf("a contracts-free system should reference no project:\n%s", csproj)
	}

	system, err := os.ReadFile(filepath.Join(outDir, "TransSystem.cs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`builder.AddTransactionalIntegration("erp-sync")`,
		".From(Ports.ErpSource)",
		".To(Ports.ErpDestination)",
	} {
		if !strings.Contains(string(system), want) {
			t.Errorf("TransSystem.cs missing %q:\n%s", want, system)
		}
	}

	// The dependency's condition is false without topics: nothing is
	// scaffolded beside the host.
	if _, err := os.Stat(filepath.Join(ws, "Contracts")); !os.IsNotExist(err) {
		t.Errorf("no contracts project should be scaffolded, stat err = %v", err)
	}

	if !strings.Contains(stderr.String(), `assembled system "trans": 1 component(s), 0 topic(s), 2 port(s), no contracts project`) {
		t.Errorf("stderr = %s", stderr.String())
	}

	var result CreateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output JSON: %v\n%s", err, stdout.String())
	}
	if result.System.SharedLibrary != "" {
		t.Errorf("SharedLibrary = %q, want empty", result.System.SharedLibrary)
	}
	if len(result.System.Topics) != 0 || len(result.System.Ports) != 2 {
		t.Errorf("result.System = %+v", result.System)
	}
}

func TestCreateScaffoldsContractsForTopicSystemWithoutOne(t *testing.T) {
	srv, _ := newSystemHostServer(t, "v1", systemHostFiles())
	defer srv.Close()

	// A topic-bearing workspace whose extractor lost (or never had) its
	// contracts sibling: the host template's dependency supplies it.
	ws := t.TempDir()
	writeBlock(t, filepath.Join(ws, "order-extractor"), template.BlockKindExtractor, "order-extractor")

	outDir := filepath.Join(ws, "system-host")
	_, stderr, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1"})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	record, err := template.LoadScaffold(filepath.Join(ws, "Contracts", filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("dependency scaffold record: %v", err)
	}
	if record.Role != template.RoleSharedLibrary || record.Template != "shared-contracts" {
		t.Errorf("dependency record = %+v", record)
	}

	topics, err := os.ReadFile(filepath.Join(outDir, "Topics.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(topics), "TopicRef<Order>") {
		t.Errorf("Topics.cs should declare the topic:\n%s", topics)
	}
	if !strings.Contains(stderr.String(), "no contracts project") {
		t.Errorf("stderr should report the workspace had no contracts project:\n%s", stderr.String())
	}
}

func TestCreateRejectsPreFactsTemplateRelease(t *testing.T) {
	files := systemHostFiles()
	files["system-host/template.yaml"] = systemHostTemplateLegacyYAML
	srv, _ := newSystemHostServer(t, "v1", files)
	defer srv.Close()

	ws := writeWorkspace(t)
	outDir := filepath.Join(ws, "system-host")
	_, _, err := runCreate(t, srv, CreateOptions{Name: "OrderFlow", StartDir: ws, OutputDir: outDir, Version: "v1"})
	if err == nil || !strings.Contains(err.Error(), "predates facts-only payloads") {
		t.Fatalf("err = %v, want the version gate's guidance", err)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Errorf("the gate must run before any output is written, stat err = %v", statErr)
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
