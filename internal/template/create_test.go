package template

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testTemplateYAML = `apiVersion: intropy.io/v1
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

// loaderTemplateYAML mirrors the library's loader shape: the scalar message
// wiring parameter can be suggested from workspace publications.
const loaderTemplateYAML = `apiVersion: intropy.io/v1
kind: Template
metadata:
  name: loader
  labels:
    intropy.io/block-kind: loader
    intropy.io/message-params: subscribes
spec:
  parameters:
    type: object
    required: [subscribes]
    properties:
      subscribes:
        type: string
`

func TestCreatePrefillsWiringFromWorkspaceFacts(t *testing.T) {
	lib := newTestLibrary(t, "v1", map[string]string{
		"loader/template.yaml":           loaderTemplateYAML,
		"loader/skeleton/README.md.tmpl": "{{ .subscribes }}\n",
	})

	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{KeyPublishes: "orders"}},
	})
	facts.SetMessageParameters([]string{KeySubscribes})

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
	if !strings.Contains(stderr.String(), "subscribes: orders") {
		t.Errorf("stderr missing subscribes prefill:\n%s", stderr.String())
	}
	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if record.Values[KeySubscribes] != "orders" {
		t.Errorf("recorded values = %v", record.Values)
	}
}

func TestCreateSetOverridesPrefill(t *testing.T) {
	lib := newTestLibrary(t, "v1", map[string]string{
		"loader/template.yaml":           loaderTemplateYAML,
		"loader/skeleton/README.md.tmpl": "{{ .subscribes }}\n",
	})

	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		{BlockKind: BlockKindExtractor, Values: map[string]any{KeyPublishes: "orders"}},
	})

	outDir := filepath.Join(t.TempDir(), "shipment-loader")
	err := Create(context.Background(), CreateOptions{
		Template:  "loader",
		OutputDir: outDir,
		Version:   "v1",
		SetValues: map[string]any{KeySubscribes: "shipments"},
		NoInput:   true,
		Stdin:     strings.NewReader(""),
		Stderr:    io.Discard,
		Source:    lib.sourceOpts(t.TempDir(), nil),
		Facts:     facts,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if record.Values[KeySubscribes] != "shipments" {
		t.Errorf("recorded values = %v", record.Values)
	}
}

const messageTemplateYAML = `apiVersion: intropy.io/v1
kind: Template
metadata:
  name: message-loader
  labels:
    intropy.io/block-kind: loader
    intropy.io/message-params: subscribes
spec:
  parameters:
    type: object
    required: [integrationName, subscribes]
    properties:
      integrationName:
        type: string
      subscribes:
        type: string
`

func newMessageTemplateLibrary(t *testing.T, tag string) *testLibrary {
	t.Helper()
	return newTestLibrary(t, tag, map[string]string{
		"message-loader/template.yaml":           messageTemplateYAML,
		"message-loader/skeleton/README.md.tmpl": "{{ .integrationName }} subscribes {{ .subscribes }}\n",
	})
}

func TestCreateSubscribeWritesScalarMessage(t *testing.T) {
	lib := newMessageTemplateLibrary(t, "v9.9.9")
	outDir := filepath.Join(t.TempDir(), "order-loader")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "message-loader",
		OutputDir:        outDir,
		Version:          "v9.9.9",
		SetValues:        map[string]any{"integrationName": "orders"},
		NoInput:          true,
		Stderr:           &stderr,
		Source:           lib.sourceOpts(t.TempDir(), nil),
		SubscribeMessage: "order-created",
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Values[KeySubscribes]; got != "order-created" {
		t.Errorf("values.subscribes = %v, want order-created", got)
	}
	rendered, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "order-created") {
		t.Errorf("render = %q, want the wired message", rendered)
	}
}

func TestCreateSubscribeRejectsConflictingMessageValue(t *testing.T) {
	lib := newMessageTemplateLibrary(t, "v9.9.9")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "message-loader",
		OutputDir:        filepath.Join(t.TempDir(), "out"),
		Version:          "v9.9.9",
		SetValues:        map[string]any{"integrationName": "orders", "subscribes": "other-message"},
		NoInput:          true,
		Stderr:           &stderr,
		Source:           lib.sourceOpts(t.TempDir(), nil),
		SubscribeMessage: "order-created",
	})
	if !errors.Is(err, ErrSubscribeMessageConflict) {
		t.Fatalf("err = %v, want ErrSubscribeMessageConflict", err)
	}
	for _, want := range []string{"other-message", "order-created"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

func TestCreateSubscribeGate(t *testing.T) {
	lib := newTemplateLibrary(t, "v9.9.9")
	var stderr bytes.Buffer

	err := Create(context.Background(), CreateOptions{
		Template:         "test-template",
		OutputDir:        filepath.Join(t.TempDir(), "out"),
		Version:          "v9.9.9",
		SetValues:        map[string]any{"integrationName": "orders"},
		NoInput:          true,
		Stderr:           &stderr,
		Source:           lib.sourceOpts(t.TempDir(), nil),
		SubscribeMessage: "order-created",
	})
	if !errors.Is(err, ErrNoMessageParameters) {
		t.Fatalf("err = %v, want ErrNoMessageParameters", err)
	}
}

const messageExtractorYAML = `apiVersion: intropy.io/v1
kind: Template
metadata:
  name: message-extractor
  labels:
    intropy.io/block-kind: extractor
    intropy.io/message-params: publishes
spec:
  parameters:
    type: object
    required: [integrationName, publishes]
    properties:
      integrationName:
        type: string
      publishes:
        type: string
`

const messageTransactionalYAML = `apiVersion: intropy.io/v1
kind: Template
metadata:
  name: message-transactional
  labels:
    intropy.io/block-kind: transactional-integration
    intropy.io/message-params: publishes
spec:
  parameters:
    type: object
    required: [integrationName, publishes]
    properties:
      integrationName:
        type: string
      publishes:
        type: string
`

func newMessageDirectionLibrary(t *testing.T, tag string) *testLibrary {
	t.Helper()
	return newTestLibrary(t, tag, map[string]string{
		"message-extractor/template.yaml":               messageExtractorYAML,
		"message-extractor/skeleton/README.md.tmpl":     "{{ .integrationName }} publishes {{ .publishes }}\n",
		"message-loader/template.yaml":                  messageTemplateYAML,
		"message-loader/skeleton/README.md.tmpl":        "{{ .integrationName }} subscribes {{ .subscribes }}\n",
		"message-transactional/template.yaml":           messageTransactionalYAML,
		"message-transactional/skeleton/README.md.tmpl": "{{ .integrationName }}\n",
	})
}

func TestCreatePublishesWritesScalarMessage(t *testing.T) {
	lib := newMessageDirectionLibrary(t, "v9.9.9")
	outDir := filepath.Join(t.TempDir(), "order-extractor")

	err := Create(context.Background(), CreateOptions{
		Template:         "message-extractor",
		OutputDir:        outDir,
		Version:          "v9.9.9",
		SetValues:        map[string]any{"integrationName": "orders"},
		NoInput:          true,
		Stderr:           io.Discard,
		Source:           lib.sourceOpts(t.TempDir(), nil),
		PublishesMessage: "order-created",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	record, err := LoadScaffold(filepath.Join(outDir, ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Values[KeyPublishes]; got != "order-created" {
		t.Errorf("values.publishes = %v, want order-created", got)
	}
}

func TestCreateMessageDirectionGate(t *testing.T) {
	lib := newMessageDirectionLibrary(t, "v9.9.9")
	for _, tc := range []struct {
		name      string
		template  string
		subscribe string
		publishes string
		wantErr   string
	}{
		{name: "subscribe against extractor", template: "message-extractor", subscribe: "m", wantErr: "use --publishes"},
		{name: "publishes against loader", template: "message-loader", publishes: "m", wantErr: "use --subscribe"},
		{name: "publishes against transactional", template: "message-transactional", publishes: "m", wantErr: "wire no messages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Create(context.Background(), CreateOptions{
				Template:         tc.template,
				OutputDir:        filepath.Join(t.TempDir(), "out"),
				Version:          "v9.9.9",
				SetValues:        map[string]any{"integrationName": "orders"},
				NoInput:          true,
				Stderr:           io.Discard,
				Source:           lib.sourceOpts(t.TempDir(), nil),
				SubscribeMessage: tc.subscribe,
				PublishesMessage: tc.publishes,
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestCreateRejectsSubscribeAndPublishesTogether(t *testing.T) {
	lib := newMessageDirectionLibrary(t, "v9.9.9")
	err := Create(context.Background(), CreateOptions{
		Template:         "message-extractor",
		OutputDir:        filepath.Join(t.TempDir(), "out"),
		Version:          "v9.9.9",
		SetValues:        map[string]any{"integrationName": "orders"},
		NoInput:          true,
		Stderr:           io.Discard,
		Source:           lib.sourceOpts(t.TempDir(), nil),
		SubscribeMessage: "m",
		PublishesMessage: "m",
	})
	if !errors.Is(err, ErrMessageFlagsExclusive) {
		t.Fatalf("err = %v, want ErrMessageFlagsExclusive", err)
	}
}

func TestCreateNoInputMessageParameterHintsDirection(t *testing.T) {
	lib := newMessageDirectionLibrary(t, "v9.9.9")
	for _, tc := range []struct{ template, flag string }{{"message-loader", "--subscribe"}, {"message-extractor", "--publishes"}} {
		t.Run(tc.template, func(t *testing.T) {
			err := Create(context.Background(), CreateOptions{
				Template:  tc.template,
				OutputDir: filepath.Join(t.TempDir(), "out"),
				Version:   "v9.9.9",
				SetValues: map[string]any{"integrationName": "orders"},
				NoInput:   true,
				Stderr:    io.Discard,
				Source:    lib.sourceOpts(t.TempDir(), nil),
				Facts:     BuildWorkspaceFacts(nil),
			})
			if err == nil || !strings.Contains(err.Error(), tc.flag) {
				t.Fatalf("err = %v, want hint %s", err, tc.flag)
			}
		})
	}
}

func TestCreateMultiMessageParameterFlagGate(t *testing.T) {
	manifest := `apiVersion: intropy.io/v1
kind: Template
metadata:
  name: multi-message
  labels:
    intropy.io/block-kind: extractor
    intropy.io/message-params: first,second
spec:
  parameters:
    type: object
    required: [integrationName, first, second]
    properties:
      integrationName: {type: string}
      first: {type: string}
      second: {type: string}
`
	lib := newTestLibrary(t, "v9.9.9", map[string]string{
		"multi-message/template.yaml":           manifest,
		"multi-message/skeleton/README.md.tmpl": "{{ .integrationName }}\n",
	})
	err := Create(context.Background(), CreateOptions{
		Template:         "multi-message",
		OutputDir:        filepath.Join(t.TempDir(), "out"),
		Version:          "v9.9.9",
		SetValues:        map[string]any{"integrationName": "orders"},
		NoInput:          true,
		Stderr:           io.Discard,
		Source:           lib.sourceOpts(t.TempDir(), nil),
		PublishesMessage: "order-created",
	})
	if err == nil || !strings.Contains(err.Error(), "declares 2 message parameters") {
		t.Fatalf("err = %v, want multi-message gate", err)
	}
}
