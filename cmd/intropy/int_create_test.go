package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/template/templatetest"
	"github.com/integrio-intropy/intropy-cli/internal/xregistry"
)

func TestSeedOrganization(t *testing.T) {
	t.Run("config organization seeds an empty fact index", func(t *testing.T) {
		withConfig(t, "organization: integrio\n")
		facts := template.BuildWorkspaceFacts(nil)
		seedOrganization(facts)
		if got, ok := facts.Organization(); !ok || got != "integrio" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})

	t.Run("workspace records beat the config", func(t *testing.T) {
		withConfig(t, "organization: integrio\n")
		facts := template.BuildWorkspaceFacts([]template.WorkspaceFactEntry{
			{BlockKind: template.BlockKindExtractor, Values: map[string]any{"organization": "acme"}},
		})
		seedOrganization(facts)
		if got, ok := facts.Organization(); !ok || got != "acme" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})

	t.Run("no config file leaves the fact unset", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		facts := template.BuildWorkspaceFacts(nil)
		seedOrganization(facts)
		if got, ok := facts.Organization(); ok || got != "" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})
}

func TestResolveCreateName(t *testing.T) {
	t.Run("name only defaults output and sets name", func(t *testing.T) {
		sets := map[string]any{}
		out, err := resolveCreateName("orders", "", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "orders" {
			t.Errorf("output = %q, want %q", out, "orders")
		}
		if sets["name"] != "orders" {
			t.Errorf("sets[name] = %v, want %q", sets["name"], "orders")
		}
	})

	t.Run("pascal name defaults a kebab output", func(t *testing.T) {
		// The sys create convention: a name and its kebab form are one
		// component, so OrderSync and order-sync scaffold the same directory.
		sets := map[string]any{}
		out, err := resolveCreateName("OrderSync", "", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "order-sync" {
			t.Errorf("output = %q, want %q", out, "order-sync")
		}
		if sets["name"] != "OrderSync" {
			t.Errorf("sets[name] = %v, want the verbatim name", sets["name"])
		}
	})

	t.Run("explicit output is kept, name still set", func(t *testing.T) {
		// Mirrors `dotnet new`: -o is the literal output location; -n never
		// nests a subdirectory under it.
		sets := map[string]any{}
		out, err := resolveCreateName("orders", "./elsewhere", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "./elsewhere" {
			t.Errorf("output = %q, want %q", out, "./elsewhere")
		}
		if sets["name"] != "orders" {
			t.Errorf("sets[name] = %v, want %q", sets["name"], "orders")
		}
	})

	t.Run("name plus --set name conflict is a usage error", func(t *testing.T) {
		sets := map[string]any{"name": "bar"}
		_, err := resolveCreateName("foo", "", sets)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
		if sets["name"] != "bar" {
			t.Errorf("sets[name] mutated on conflict: %v", sets["name"])
		}
	})

	t.Run("no name is a passthrough", func(t *testing.T) {
		sets := map[string]any{}
		out, err := resolveCreateName("", "./out", sets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "./out" {
			t.Errorf("output = %q, want %q", out, "./out")
		}
		if _, ok := sets["name"]; ok {
			t.Errorf("sets should be untouched, got %v", sets)
		}
	})
}

// outDirLibrary builds a git-backed library holding one template whose
// manifest declares the given extra parameter block, for deriveOutDir's fetch.
func outDirLibrary(t *testing.T, extraParams string) *templatetest.Library {
	t.Helper()
	manifest := `apiVersion: intropy.io/v1
kind: Template
metadata:
  name: hello-world
spec:
  parameters:
    type: object
    required: [integrationName]
    properties:
      integrationName:
        type: string
`
	manifest += extraParams
	return templatetest.NewLibrary(t, "v1", map[string]string{
		"hello-world/template.yaml":           manifest,
		"hello-world/skeleton/README.md.tmpl": "{{ .integrationName }}\n",
	})
}

func deriveOutDirOpts(t *testing.T, lib *templatetest.Library, sets map[string]any) template.CreateOptions {
	t.Helper()
	var stderr bytes.Buffer
	return template.CreateOptions{
		Template:  "hello-world",
		SetValues: sets,
		NoInput:   true,
		Stderr:    &stderr,
		Source:    lib.Source(t),
	}
}

func TestDeriveOutDir(t *testing.T) {
	nameParam := "      name:\n        type: string\n"

	t.Run("resolved name kebab-cases into the directory", func(t *testing.T) {
		lib := outDirLibrary(t, nameParam)
		opts := deriveOutDirOpts(t, lib, map[string]any{"integrationName": "x", "name": "OrderSync"})
		out, err := deriveOutDir(context.Background(), opts)
		if err != nil {
			t.Fatalf("deriveOutDir: %v", err)
		}
		if out != "order-sync" {
			t.Errorf("out = %q, want order-sync", out)
		}
	})

	t.Run("no name parameter is a usage error", func(t *testing.T) {
		lib := outDirLibrary(t, "")
		_, err := deriveOutDir(context.Background(), deriveOutDirOpts(t, lib, map[string]any{"integrationName": "x"}))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("error %v is not a usageError", err)
		}
	})
}

func TestIntCreateOutputValidation(t *testing.T) {
	resetCreateFlags := func(t *testing.T) {
		t.Helper()
		intCreateFlags = createFlags{}
		t.Cleanup(func() { intCreateFlags = createFlags{} })
	}

	t.Run("non-json --output is a usage error", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--output", "./out", "--name", "x", "--no-input"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid output format") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestWorkspaceRootOf(t *testing.T) {
	t.Run("bare name scans the working directory", func(t *testing.T) {
		if got := workspaceRootOf("orders-api"); got != "." {
			t.Errorf("workspaceRootOf = %q, want .", got)
		}
	})
	t.Run("nested output scans its parent", func(t *testing.T) {
		if got := workspaceRootOf("systems/acme/orders-api"); got != "systems/acme" {
			t.Errorf("workspaceRootOf = %q, want systems/acme", got)
		}
	})
	t.Run("empty scans the working directory", func(t *testing.T) {
		if got := workspaceRootOf(""); got != "." {
			t.Errorf("workspaceRootOf = %q, want .", got)
		}
	})
}

// messageRegistryFixture serves an /export shaped like the Intropy registry
// for the --subscribe e2e tests.
func messageRegistryFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
  "specversion": "1.0-rc2x",
  "registryid": "intropy",
  "messagegroups": {
    "io.intropy.maxbo.product": {
      "messagegroupid": "io.intropy.maxbo.product",
      "messages": {
        "io.intropy.maxbo.product.export": {
          "messageid": "io.intropy.maxbo.product.export",
          "versions": {
            "1": {
              "versionid": "1",
              "isdefault": true,
              "envelope": "CloudEvents/1.0",
              "envelopemetadata": {"type": {"value": "io.intropy.maxbo.product.export", "required": true}},
              "dataschemaxid": "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1"
            }
          }
        }
      }
    }
  },
  "endpoints": {
    "perfion-extractor": {
      "endpointid": "perfion-extractor",
      "usage": ["producer"],
      "channel": "product-distribution-pubsub/sbt-test-product-extractor-001",
      "messagegroups": ["/messagegroups/io.intropy.maxbo.product"]
    }
  },
  "schemagroups": {
    "io.intropy.maxbo.product": {
      "schemagroupid": "io.intropy.maxbo.product",
      "schemas": {
        "product-export.v1": {
          "schemaid": "product-export.v1",
          "versions": {
            "1": {"versionid": "1", "isdefault": true, "xid": "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1"}
          }
        }
      }
    }
  }
}`)
	})
	svc := httptest.NewServer(mux)
	t.Cleanup(svc.Close)
	return svc
}

// messageLoaderLibrary builds a template library whose loader declares a
// message parameter through the message label — what the template PR will
// ship — so the full AE1 flow can run end to end.
func messageLoaderLibrary(t *testing.T) *templatetest.Library {
	t.Helper()
	manifest := `apiVersion: intropy.dev/v1
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
	return templatetest.NewLibrary(t, "v1", map[string]string{
		"message-loader/template.yaml":           manifest,
		"message-loader/skeleton/README.md.tmpl": "{{ .integrationName }} subscribes {{ .message }}\n",
	})
}

// AE1 end to end at the CLI seam: resolveSubscribeBlock turns --subscribe
// into the block from the fixture registry; the record carries it in full
// — pubsub, topic, CloudEvents type, logical dataschema, pinned
// default-version URL.
func TestIntCreateSubscribeEndToEnd(t *testing.T) {
	registry := messageRegistryFixture(t)
	t.Setenv("INTROPY_REGISTRY_URL", registry.URL)

	manifest := messageLoaderLibrary(t)
	outDir := filepath.Join(t.TempDir(), "order-loader")

	stderr := &bytes.Buffer{}
	block, err := resolveSubscribeBlock(context.Background(), "io.intropy.maxbo.product.export", "", true, stderr)
	if err != nil {
		t.Fatalf("resolveSubscribeBlock: %v\nstderr: %s", err, stderr.String())
	}

	err = template.Create(context.Background(), template.CreateOptions{
		Template:  "message-loader",
		OutputDir: outDir,
		Version:   "v1",
		SetValues: map[string]any{"integrationName": "orders", "message": "io.intropy.maxbo.product.export"},
		NoInput:   true,
		Stderr:    stderr,
		Source:    manifest.Source(t),
		Subscribe: block,
	})
	if err != nil {
		t.Fatalf("Create: %v\nstderr: %s", err, stderr.String())
	}

	record, err := template.LoadScaffold(filepath.Join(outDir, template.ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	entry := template.ScaffoldEntry{Path: outDir, Scaffold: *record}
	b, err := template.ReadSubscribeBlock(entry)
	if err != nil {
		t.Fatalf("scaffold record is not block-shaped: %v (values: %v)", err, record.Values)
	}
	want := template.SubscribeBlock{
		Message:       "io.intropy.maxbo.product.export",
		Pubsub:        "product-distribution-pubsub",
		Topic:         "sbt-test-product-extractor-001",
		Dataschema:    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
		DataschemaURL: registry.URL + "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
	}
	if *b != want {
		t.Errorf("subscribe block = %+v, want %+v", *b, want)
	}
}

// AE2 through the CLI error mapping: the gate surfaces as a usage error
// (exit 2), naming the template.
func TestIntCreateSubscribeGateIsUsageError(t *testing.T) {
	registry := messageRegistryFixture(t)
	t.Setenv("INTROPY_REGISTRY_URL", registry.URL)

	lib := outDirLibrary(t, "") // no message label
	stderr := &bytes.Buffer{}
	block, err := resolveSubscribeBlock(context.Background(), "io.intropy.maxbo.product.export", "", true, stderr)
	if err != nil {
		t.Fatalf("resolveSubscribeBlock: %v", err)
	}

	err = template.Create(context.Background(), template.CreateOptions{
		Template:  "hello-world",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Version:   "v1",
		SetValues: map[string]any{"integrationName": "orders"},
		NoInput:   true,
		Stderr:    stderr,
		Source:    lib.Source(t),
		Subscribe: block,
	})
	if !errors.Is(err, template.ErrNoMessageParameters) {
		t.Fatalf("err = %v, want ErrNoMessageParameters", err)
	}
	uerr := usageIfMessageGateError(err)
	if _, ok := uerr.(*usageError); !ok {
		t.Errorf("usageIfMessageGateError = %T, want *usageError", uerr)
	}
}

func TestIntCreateSubscribeConflictIsUsageError(t *testing.T) {
	err := usageIfMessageGateError(fmt.Errorf("template: %w", template.ErrSubscribeMessageConflict))
	if _, ok := err.(*usageError); !ok {
		t.Errorf("usageIfMessageGateError = %T, want *usageError", err)
	}
}

func TestRegistryMessageRefsUnconfiguredIsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTROPY_REGISTRY_URL", "")

	refs, err := registryMessageRefs(context.Background(), "")
	if err != nil {
		t.Fatalf("registryMessageRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want none", refs)
	}
}

// registryFixtureWithEndpoints renders the AE1 export with a caller chosen
// producer endpoint set, so the no-producer and multi-producer remediation
// paths run against a registry otherwise identical to the happy fixture.
func registryFixtureWithEndpoints(t *testing.T, endpointsJSON string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "specversion": "1.0-rc2x",
  "registryid": "intropy",
  "messagegroups": {
    "io.intropy.maxbo.product": {
      "messagegroupid": "io.intropy.maxbo.product",
      "messages": {
        "io.intropy.maxbo.product.export": {
          "messageid": "io.intropy.maxbo.product.export",
          "versions": {"1": {"versionid": "1", "isdefault": true, "envelope": "CloudEvents/1.0", "envelopemetadata": {"type": {"value": "io.intropy.maxbo.product.export", "required": true}}, "dataschemaxid": "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1"}}
        }
      }
    }
  },
  "endpoints": %s,
  "schemagroups": {
    "io.intropy.maxbo.product": {
      "schemagroupid": "io.intropy.maxbo.product",
      "schemas": {
        "product-export.v1": {
          "schemaid": "product-export.v1",
          "versions": {"1": {"versionid": "1", "isdefault": true, "xid": "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1"}}
        }
      }
    }
  }
}`, endpointsJSON)
	})
	svc := httptest.NewServer(mux)
	t.Cleanup(svc.Close)
	return svc
}

func messageExtractorLibrary(t *testing.T) *templatetest.Library {
	t.Helper()
	manifest := `apiVersion: intropy.dev/v1
kind: Template
metadata:
  name: message-extractor
  labels:
    intropy.dev/block-kind: extractor
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
	return templatetest.NewLibrary(t, "v1", map[string]string{
		"message-extractor/template.yaml":           manifest,
		"message-extractor/skeleton/README.md.tmpl": "{{ .integrationName }} publishes {{ .message }}\n",
	})
}

// AE1 through the publish path: the resolved block carries the producing
// channel's pubsub and topic, the logical dataschema, and the pinned
// default-version URL joined with the registry base.
func TestIntCreatePublishesEndToEnd(t *testing.T) {
	registry := messageRegistryFixture(t)
	t.Setenv("INTROPY_REGISTRY_URL", registry.URL)

	manifest := messageExtractorLibrary(t)
	outDir := filepath.Join(t.TempDir(), "erp-extractor")

	block, err := resolvePublishesBlock(context.Background(), "io.intropy.maxbo.product.export", "")
	if err != nil {
		t.Fatalf("resolvePublishesBlock: %v", err)
	}

	err = template.Create(context.Background(), template.CreateOptions{
		Template:  "message-extractor",
		OutputDir: outDir,
		Version:   "v1",
		SetValues: map[string]any{"integrationName": "erp", "message": "io.intropy.maxbo.product.export"},
		NoInput:   true,
		Stderr:    io.Discard,
		Source:    manifest.Source(t),
		Publishes: block,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	record, err := template.LoadScaffold(filepath.Join(outDir, template.ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	b, err := template.ReadPublishesBlock(template.ScaffoldEntry{Path: outDir, Scaffold: *record})
	if err != nil {
		t.Fatalf("scaffold record is not block-shaped: %v", err)
	}
	want := template.PublishesBlock{
		Message:       "io.intropy.maxbo.product.export",
		Pubsub:        "product-distribution-pubsub",
		Topic:         "sbt-test-product-extractor-001",
		Dataschema:    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
		DataschemaURL: registry.URL + "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
	}
	if *b != want {
		t.Errorf("publishes block = %+v, want %+v", *b, want)
	}
}

// AE3: --publishes refuses to guess the channel. No producer endpoint
// means the registry has not declared this component yet — the remediation
// names the missing registration, and several producers is a hard error
// listing the candidates, never a pick.
func TestIntCreatePublishesChannelFailures(t *testing.T) {
	t.Run("no producer endpoint names the registration", func(t *testing.T) {
		registry := registryFixtureWithEndpoints(t, `{}`)
		t.Setenv("INTROPY_REGISTRY_URL", registry.URL)

		_, err := resolvePublishesBlock(context.Background(), "io.intropy.maxbo.product.export", "")
		var no *xregistry.NoProducerError
		if !errors.As(err, &no) {
			t.Fatalf("err = %v, want NoProducerError", err)
		}
		if !strings.Contains(err.Error(), "register the producing endpoint") {
			t.Errorf("error %q should name the registration remediation", err)
		}
	})

	t.Run("several producers is a hard error naming channels", func(t *testing.T) {
		registry := registryFixtureWithEndpoints(t, `{
    "perfion-extractor": {"endpointid": "perfion-extractor", "usage": ["producer"], "channel": "product-distribution-pubsub/sbt-test-product-extractor-001", "messagegroups": ["/messagegroups/io.intropy.maxbo.product"]},
    "erp-extractor": {"endpointid": "erp-extractor", "usage": ["producer"], "channel": "erp-distribution-pubsub/sbt-test-erp-extractor-001", "messagegroups": ["/messagegroups/io.intropy.maxbo.product"]}
  }`)
		t.Setenv("INTROPY_REGISTRY_URL", registry.URL)

		_, err := resolvePublishesBlock(context.Background(), "io.intropy.maxbo.product.export", "")
		var amb *xregistry.AmbiguousProducerError
		if !errors.As(err, &amb) {
			t.Fatalf("err = %v, want AmbiguousProducerError", err)
		}
		for _, channel := range []string{
			"product-distribution-pubsub/sbt-test-product-extractor-001",
			"erp-distribution-pubsub/sbt-test-erp-extractor-001",
			"register the endpoint for this component",
		} {
			if !strings.Contains(err.Error(), channel) {
				t.Errorf("error %q should name %q", err, channel)
			}
		}
	})
}

func TestIntCreatePublishesFlagCombinations(t *testing.T) {
	resetCreateFlags := func(t *testing.T) {
		t.Helper()
		intCreateFlags = createFlags{}
		t.Cleanup(func() { intCreateFlags = createFlags{} })
	}

	t.Run("both message flags are a usage error", func(t *testing.T) {
		resetCreateFlags(t)
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--name", "x", "--no-input", "--subscribe", "a.b", "--publishes", "a.b"})
		err := rootCmd.Execute()
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("error %v is not a usageError", err)
		}
		if !strings.Contains(err.Error(), "cannot combine --subscribe with --publishes") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unconfigured registry fails before any prompt or fetch", func(t *testing.T) {
		resetCreateFlags(t)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("INTROPY_REGISTRY_URL", "")
		var stdout, stderr bytes.Buffer
		resetRootIO(t, &stdout, &stderr)
		t.Chdir(t.TempDir())

		rootCmd.SetArgs([]string{"int", "create", "hello-world", "--name", "x", "--no-input", "--publishes", "io.intropy.maxbo.product.export"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "registryUrl") && !strings.Contains(err.Error(), "registry") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// The direction and conflict gates exit 2 through the shared mapper.
func TestIntCreatePublishesGatesAreUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"direction", fmt.Errorf("template: %w", template.ErrMessageDirection)},
		{"publishes conflict", fmt.Errorf("template: %w", template.ErrMessageParameterConflict)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uerr := usageIfMessageGateError(tc.err)
			if _, ok := uerr.(*usageError); !ok {
				t.Errorf("usageIfMessageGateError = %T, want *usageError", uerr)
			}
		})
	}
}

// An unconfigured registry warns before the pool loads: an empty pool
// must read as "no registry configured", not "no messages exist".
func TestPromptMessageRefsLoaderWarnsUnconfigured(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTROPY_REGISTRY_URL", "")
	var stderr bytes.Buffer

	loader := promptMessageRefsLoader(true, "", &stderr)
	refs, err := loader(context.Background())
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want none", refs)
	}
	if !strings.Contains(stderr.String(), "no registryUrl configured") {
		t.Errorf("stderr %q should carry the unconfigured warning", stderr.String())
	}
	// The memo holds: a second load neither reloads nor re-warns.
	refs, err = loader(context.Background())
	if err != nil || len(refs) != 0 {
		t.Fatalf("second load = %v, %v", refs, err)
	}
	if n := strings.Count(stderr.String(), "no registryUrl configured"); n != 1 {
		t.Errorf("warnings = %d, want the memoized one", n)
	}
}

// A loader failure now propagates: the create flow degrades it to a
// warning beside the workspace-only pool, the CLI no longer masks it here.
func TestPromptMessageRefsLoaderPropagatesErrors(t *testing.T) {
	registry := messageRegistryFixture(t)
	t.Setenv("INTROPY_REGISTRY_URL", registry.URL)
	var stderr bytes.Buffer

	loader := promptMessageRefsLoader(true, registry.URL+"/broken", &stderr)
	if _, err := loader(context.Background()); err == nil {
		t.Fatal("the failed export fetch must propagate to the caller that degrades it")
	}
}
