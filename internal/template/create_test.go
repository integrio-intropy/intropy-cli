package template

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: test-template
  title: Test
spec:
  parameters:
    type: object
    required: [integrationName]
    properties:
      integrationName:
        type: string
      namespace:
        type: string
        default: default
`

// newTemplateLibrary builds a single-template library laid out as the v1
// model expects: <template>/template.yaml plus <template>/skeleton/<files>.
func newTemplateLibrary(t *testing.T, tag string) *testLibrary {
	t.Helper()
	return newTestLibrary(t, tag, map[string]string{
		"test-template/template.yaml":           testTemplateYAML,
		"test-template/skeleton/README.md.tmpl": "{{ .integrationName }} in {{ .namespace }}\n",
	})
}

func TestCreateWritesOutputJSON(t *testing.T) {
	lib := newTemplateLibrary(t, "v9.9.9")

	outDir := filepath.Join(t.TempDir(), "out")
	jsonPath := filepath.Join(t.TempDir(), "result.json")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:   "test-template",
		OutputDir:  outDir,
		Version:    "v9.9.9",
		SetValues:  map[string]any{"integrationName": "orders"},
		NoInput:    true,
		OutputJSON: jsonPath,
		Stderr:     &stderr,
		Source:     lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var got CreateResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, string(data))
	}
	if got.Template != "test-template" {
		t.Errorf("Template = %q", got.Template)
	}
	if got.Owner != defaultTemplateOwner || got.Repo != defaultTemplateRepo {
		t.Errorf("Owner/Repo = %q/%q, want the resolved defaults", got.Owner, got.Repo)
	}
	if got.Version != "v9.9.9" {
		t.Errorf("Version = %q", got.Version)
	}
	if !filepath.IsAbs(got.OutputDir) {
		t.Errorf("OutputDir should be absolute: %q", got.OutputDir)
	}
	if got.Values["integrationName"] != "orders" {
		t.Errorf("values[integrationName] = %v", got.Values["integrationName"])
	}
	if got.Values["namespace"] != "default" {
		t.Errorf("values[namespace] = %v (default should layer in)", got.Values["namespace"])
	}
}

func TestCreateOnManifestAbortsBeforeOutput(t *testing.T) {
	lib := newTemplateLibrary(t, "v9.9.9")

	outDir := filepath.Join(t.TempDir(), "out")
	var stderr bytes.Buffer
	gate := errors.New("gate says no")
	called := false

	err := Create(context.Background(), CreateOptions{
		Template:  "test-template",
		OutputDir: outDir,
		Version:   "v9.9.9",
		SetValues: map[string]any{"integrationName": "orders"},
		NoInput:   true,
		Stderr:    &stderr,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		OnManifest: func(tmpl *Template) error {
			called = true
			if tmpl.Metadata.Name != "test-template" {
				t.Errorf("hook saw manifest %q, want test-template", tmpl.Metadata.Name)
			}
			return gate
		},
	})
	if !errors.Is(err, gate) {
		t.Fatalf("err = %v, want the gate error", err)
	}
	if !called {
		t.Fatal("OnManifest was never called")
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Errorf("a failed hook must abort before any output; %s exists", outDir)
	}
}

func TestCreateWritesScaffoldFile(t *testing.T) {
	lib := newTemplateLibrary(t, "v2.0.0")

	outDir := filepath.Join(t.TempDir(), "out")
	err := Create(context.Background(), CreateOptions{
		Template:  "test-template",
		OutputDir: outDir,
		Version:   "v2.0.0",
		SetValues: map[string]any{"integrationName": "orders"},
		NoInput:   true,
		Stderr:    &bytes.Buffer{},
		Source:    lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := LoadScaffold(filepath.Join(outDir, filepath.FromSlash(ScaffoldRelPath)))
	if err != nil {
		t.Fatalf("LoadScaffold: %v", err)
	}
	if got.SchemaVersion != ScaffoldSchemaVersion {
		t.Errorf("SchemaVersion = %d", got.SchemaVersion)
	}
	if got.Template != "test-template" || got.Owner != defaultTemplateOwner || got.Repo != defaultTemplateRepo || got.Version != "v2.0.0" {
		t.Errorf("scaffold identity = %q %q/%q@%q, want the resolved defaults", got.Template, got.Owner, got.Repo, got.Version)
	}
	if got.Values["integrationName"] != "orders" || got.Values["namespace"] != "default" {
		t.Errorf("Values = %v", got.Values)
	}
}

func TestCreateOutputJSONStdout(t *testing.T) {
	lib := newTemplateLibrary(t, "v1")

	outDir := filepath.Join(t.TempDir(), "out")
	var stdout, stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:   "test-template",
		OutputDir:  outDir,
		Version:    "v1",
		SetValues:  map[string]any{"integrationName": "x"},
		NoInput:    true,
		OutputJSON: "-",
		Stdout:     &stdout,
		Stderr:     &stderr,
		Source:     lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(stdout.String(), `"template": "test-template"`) {
		t.Errorf("stdout missing template field: %s", stdout.String())
	}
	// Human-readable logs must stay on stderr so stdout is pure JSON.
	if strings.Contains(stdout.String(), "fetching") {
		t.Errorf("stdout should not contain log lines: %s", stdout.String())
	}
}

func TestCreateDoesNotCreateOutputDirWhenValidationFails(t *testing.T) {
	lib := newTemplateLibrary(t, "v1")

	outDir := filepath.Join(t.TempDir(), "out")
	err := Create(context.Background(), CreateOptions{
		Template:  "test-template",
		OutputDir: outDir,
		Version:   "v1",
		NoInput:   true,
		Stderr:    &bytes.Buffer{},
		Source:    lib.sourceOpts(t.TempDir(), nil),
	})
	if err == nil || !strings.Contains(err.Error(), "missing required parameter") {
		t.Fatalf("expected missing required parameter error, got %v", err)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("failed create should not create output dir, stat err=%v", statErr)
	}
}

func TestCreateReadsStdinValues(t *testing.T) {
	lib := newTemplateLibrary(t, "v1")

	outDir := filepath.Join(t.TempDir(), "out")
	jsonPath := filepath.Join(t.TempDir(), "result.json")

	err := Create(context.Background(), CreateOptions{
		Template:   "test-template",
		OutputDir:  outDir,
		Version:    "v1",
		Files:      []string{StdinValuesPath},
		NoInput:    true,
		OutputJSON: jsonPath,
		Stdin:      bytes.NewBufferString(`{"integrationName": "from-stdin", "namespace": "ns2"}`),
		Stderr:     &bytes.Buffer{},
		Source:     lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(readme) != "from-stdin in ns2\n" {
		t.Errorf("README = %q", string(readme))
	}

	data, _ := os.ReadFile(jsonPath)
	if !bytes.Contains(data, []byte(`"namespace": "ns2"`)) {
		t.Errorf("result JSON missing namespace value: %s", string(data))
	}
}

func TestCreateFactsEnrichMissingParameterError(t *testing.T) {
	lib := newTemplateLibrary(t, "v1")

	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{
			"topic": "orders", "contract": "Order",
		}},
	})

	err := Create(context.Background(), CreateOptions{
		Template:  "test-template",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Version:   "v1",
		NoInput:   true,
		Stderr:    &bytes.Buffer{},
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Facts:     facts,
	})
	if err == nil {
		t.Fatal("expected missing parameter error")
	}
	if !strings.Contains(err.Error(), "missing required parameter(s): integrationName") {
		t.Errorf("error = %v", err)
	}
	// The facts carry no convention for integrationName, so no hint line.
	if strings.Contains(err.Error(), "known") {
		t.Errorf("unrelated facts should not produce hints: %v", err)
	}
}

func TestCreateWithoutFactsResolvesAsBefore(t *testing.T) {
	lib := newTemplateLibrary(t, "v1")

	outDir := filepath.Join(t.TempDir(), "out")
	err := Create(context.Background(), CreateOptions{
		Template:  "test-template",
		OutputDir: outDir,
		Version:   "v1",
		SetValues: map[string]any{"integrationName": "api"},
		NoInput:   true,
		Stderr:    &bytes.Buffer{},
		Source:    lib.sourceOpts(t.TempDir(), nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if record.Values["integrationName"] != "api" {
		t.Errorf("recorded values = %v", record.Values)
	}
}

// loaderTemplateYAML mirrors the library's loader shape: the wiring
// parameters the workspace facts convention resolves.
const loaderTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: loader
spec:
  parameters:
    type: object
    required: [topic, contract]
    properties:
      topic:
        type: string
      contract:
        type: string
      pubsub:
        type: string
        default: pubsub
`

// TestCreatePrefillsWiringFromWorkspaceFacts is the loader-in-a-system
// walkthrough end to end: the extractor's recorded wiring prefills the
// loader's required parameters with no prompt (stdin is empty and stays
// unread), the notes name each override hatch, and the scaffold record
// persists the same wiring the developer confirmed by running.
func TestCreatePrefillsWiringFromWorkspaceFacts(t *testing.T) {
	lib := newTestLibrary(t, "v1", map[string]string{
		"loader/template.yaml":           loaderTemplateYAML,
		"loader/skeleton/README.md.tmpl": "{{ .topic }} carries {{ .contract }} on {{ .pubsub }}\n",
	})

	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{
			"topic": "orders", "contract": "Order", "pubsub": "pubsub",
		}},
	})

	var stderr bytes.Buffer
	outDir := filepath.Join(t.TempDir(), "order-loader")
	err := Create(context.Background(), CreateOptions{
		Template:  "loader",
		OutputDir: outDir,
		Version:   "v1",
		NoInput:   true,
		Stdin:     strings.NewReader(""),
		Stderr:    &stderr,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Facts:     facts,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, want := range []string{
		"topic: orders (from workspace; override with --set topic=<value>)",
		"contract: Order (from workspace; override with --set contract=<value>)",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}

	rendered, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "orders carries Order on pubsub\n" {
		t.Errorf("rendered = %q", rendered)
	}

	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if record.Values["topic"] != "orders" || record.Values["contract"] != "Order" {
		t.Errorf("recorded values = %v", record.Values)
	}
}

// TestCreateSetOverridesPrefill pins the override hatch: --set topic wins
// over the workspace candidate, and a --set topic the facts do not know
// leaves contract to be supplied explicitly (NoInput makes that the clean
// missing-parameter error).
func TestCreateSetOverridesPrefill(t *testing.T) {
	lib := newTestLibrary(t, "v1", map[string]string{
		"loader/template.yaml":           loaderTemplateYAML,
		"loader/skeleton/README.md.tmpl": "{{ .topic }} carries {{ .contract }}\n",
	})

	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{
			"topic": "orders", "contract": "Order",
		}},
	})

	outDir := filepath.Join(t.TempDir(), "shipment-loader")
	err := Create(context.Background(), CreateOptions{
		Template:  "loader",
		OutputDir: outDir,
		Version:   "v1",
		SetValues: map[string]any{"topic": "shipments", "contract": "Shipment"},
		NoInput:   true,
		Stdin:     strings.NewReader(""),
		Stderr:    &bytes.Buffer{},
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Facts:     facts,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rendered, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "shipments carries Shipment\n" {
		t.Errorf("rendered = %q", rendered)
	}
}

// messageTemplateYAML declares one message-wiring parameter through the
// message label — the shape the template library's message PR will ship.
const messageTemplateYAML = `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: message-loader
  labels:
    intropy.dev/block-kind: loader
    intropy.dev/message-params: message
spec:
  parameters:
    type: object
    required: [integrationName, message]
    properties:
      integrationName:
        type: string
      message:
        type: string
`

func newMessageTemplateLibrary(t *testing.T, tag string) *testLibrary {
	t.Helper()
	return newTestLibrary(t, tag, map[string]string{
		"message-loader/template.yaml":           messageTemplateYAML,
		"message-loader/skeleton/README.md.tmpl": "{{ .integrationName }} subscribes {{ .message }}\n",
	})
}

func subscribeBlockFixture() *SubscribeBlock {
	return &SubscribeBlock{
		Message:       "io.intropy.maxbo.product.export",
		Pubsub:        "product-distribution-pubsub",
		Topic:         "sbt-test-product-extractor-001",
		Dataschema:    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
		DataschemaURL: "https://registry.example.com/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
	}
}

// AE1: --subscribe with a resolved block writes the full block into the
// scaffold record's values, and the resolution reaches the render.
func TestCreateSubscribeWritesBlock(t *testing.T) {
	lib := newMessageTemplateLibrary(t, "v9.9.9")
	outDir := filepath.Join(t.TempDir(), "order-loader")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:  "message-loader",
		OutputDir: outDir,
		Version:   "v9.9.9",
		SetValues: map[string]any{"integrationName": "orders", "message": "io.intropy.maxbo.product.export"},
		NoInput:   true,
		Stderr:    &stderr,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Subscribe: subscribeBlockFixture(),
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadSubscribeBlock(ScaffoldEntry{Path: outDir, Scaffold: *record})
	if err != nil {
		t.Fatalf("scaffold record is not the block shape: %v (values: %v)", err, record.Values)
	}
	if *b != *subscribeBlockFixture() {
		t.Errorf("record subscribe block = %+v, want %+v", *b, *subscribeBlockFixture())
	}

	rendered, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "io.intropy.maxbo.product.export") {
		t.Errorf("render = %q, want the wired message", rendered)
	}
}

// AE2: --subscribe against a template with no message parameters is a
// usage-class error naming the template, before anything renders.
func TestCreateSubscribeGate(t *testing.T) {
	lib := newTemplateLibrary(t, "v9.9.9") // no message label
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:  "test-template",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Version:   "v9.9.9",
		SetValues: map[string]any{"integrationName": "orders", "message": "io.intropy.maxbo.product.export"},
		NoInput:   true,
		Stderr:    &stderr,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Subscribe: subscribeBlockFixture(),
	})
	if !errors.Is(err, ErrNoMessageParameters) {
		t.Fatalf("err = %v, want ErrNoMessageParameters", err)
	}
	if !strings.Contains(err.Error(), "test-template") {
		t.Errorf("error %q should name the template", err)
	}

	// Nothing rendered: the gate runs before output.
	if _, statErr := os.Stat(filepath.Join(t.TempDir(), "out")); statErr == nil {
		// (out is a fresh dir; nothing from this run landed anywhere)
	}
}

// A message-capable template registers its message parameters and the
// registry refs with the facts, so prompts offer the union.
func TestCreateSubscribeFactsGetMessageCandidates(t *testing.T) {
	lib := newMessageTemplateLibrary(t, "v9.9.9")
	var stderr bytes.Buffer
	facts := BuildWorkspaceFacts(nil)

	err := Create(context.Background(), CreateOptions{
		Template:  "message-loader",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Version:   "v9.9.9",
		SetValues: map[string]any{"integrationName": "orders", "message": "io.intropy.maxbo.product.export"},
		NoInput:   true,
		Stderr:    &stderr,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Facts:     facts,
		MessageRefs: []string{
			"io.intropy.maxbo.product.export",
			"io.intropy.maxbo.catalog.updated",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !facts.IsMessageParameter("message") {
		t.Fatal("the manifest's message parameter did not reach the facts")
	}
	got := facts.MessageCandidates()
	if len(got) != 2 || got[0] != "io.intropy.maxbo.catalog.updated" {
		t.Errorf("candidates = %v, want both registry refs", got)
	}
}

// --no-input plus a required message parameter and no --subscribe fails
// naming --subscribe.
func TestCreateNoInputMessageParameterHintsSubscribe(t *testing.T) {
	lib := newMessageTemplateLibrary(t, "v9.9.9")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:  "message-loader",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Version:   "v9.9.9",
		SetValues: map[string]any{"integrationName": "orders"},
		NoInput:   true,
		Stderr:    &stderr,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		// The command always supplies the workspace facts; the parameter
		// registry that turns the hint on lives there.
		Facts: BuildWorkspaceFacts(nil),
	})
	if err == nil {
		t.Fatal("expected the missing-required error")
	}
	if !strings.Contains(err.Error(), "--subscribe") {
		t.Errorf("error %q should name --subscribe", err)
	}
	if !strings.Contains(err.Error(), "message") {
		t.Errorf("error %q should name the message parameter", err)
	}
}
