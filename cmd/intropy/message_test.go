package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// messageRegistryFixture serves an /export shaped like the Intropy
// registry: one message group, one message, one producer endpoint. It
// returns the fixture URL and a closer that also resets the config's
// registryUrl environment override.
func messageRegistryURLFixture(t *testing.T) string {
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
	tsvc := httptest.NewServer(mux)
	t.Cleanup(tsvc.Close)
	return tsvc.URL
}

// withRegistryURL points the config environment at the fixture for the
// duration of the test.
func withRegistryURL(t *testing.T, url string) {
	t.Helper()
	t.Setenv("INTROPY_REGISTRY_URL", url)
}

func runMessage(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	rootCmd.SetArgs(append([]string{"message"}, args...))
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute message %v: %v", args, err)
	}
	return
}

func TestMessageListFixtureRegistry(t *testing.T) {
	url := messageRegistryURLFixture(t)
	withRegistryURL(t, url)
	t.Chdir(t.TempDir())

	stdout, _ := runMessage(t, "list", "--output", "plain")
	out := stdout.String()
	if !strings.Contains(out, "io.intropy.maxbo.product.export") ||
		!strings.Contains(out, "io.intropy.maxbo.product") ||
		!strings.Contains(out, "product-distribution-pubsub/sbt-test-product-extractor-001") ||
		!strings.Contains(out, "product-export.v1") {
		t.Errorf("list output = %q", out)
	}
}

func TestMessageListGroupFilter(t *testing.T) {
	url := messageRegistryURLFixture(t)
	withRegistryURL(t, url)
	t.Chdir(t.TempDir())

	tmp := t.TempDir()
	writeScaffoldT(t, tmp+"/orders", "loader", "v0.4.0")

	// --group hides workspace messages: they carry no group.
	_, stderr := runMessage(t, "list", "--group", "io.intropy.other")
	if !strings.Contains(stderr.String(), "no messages found") {
		t.Errorf("narrowing to an absent group should print the empty state, got %q", stderr.String())
	}
}

func TestMessageListWorkspaceMessages(t *testing.T) {
	withRegistryURL(t, messageRegistryURLFixture(t))
	tmp := t.TempDir()
	t.Chdir(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".intropy"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, tmp+"/.intropy/scaffold.json", `{"schemaVersion":1,"template":"product-sink","owner":"o","repo":"r","version":"v1","blockKind":"extractor","values":{"appId":"product-sink","publishes":{"message":"product-exported","contract":"ProductExported"}}}`+"\n")

	stdout, _ := runMessage(t, "list", "--group", "", "--output", "plain")
	out := stdout.String()
	if !strings.Contains(out, "product-exported") || !strings.Contains(out, "workspace") {
		t.Errorf("internal message missing from list:\n%s", out)
	}
	if !strings.Contains(out, "io.intropy.maxbo.product.export") {
		t.Errorf("registry message missing from merged list:\n%s", out)
	}
}

func TestMessageListJSONOutput(t *testing.T) {
	withRegistryURL(t, messageRegistryURLFixture(t))
	t.Chdir(t.TempDir())

	stdout, _ := runMessage(t, "list", "--group", "", "--output", "json")
	var entries []MessageEntry
	if err := json.Unmarshal([]byte(stdout.String()), &entries); err != nil {
		t.Fatalf("parse json: %v\n%s", err, stdout.String())
	}
	if len(entries) != 1 || entries[0].Message != "io.intropy.maxbo.product.export" {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.Source != "registry" || e.Group != "io.intropy.maxbo.product" {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Channels) != 1 || e.Channels[0] != "product-distribution-pubsub/sbt-test-product-extractor-001" {
		t.Errorf("channels = %+v", e.Channels)
	}
	if !strings.HasSuffix(e.DataSchemaURL, "/schemas/product-export.v1/versions/1") {
		t.Errorf("DataSchemaURL = %q, want the pinned default version", e.DataSchemaURL)
	}
	if e.Type != "io.intropy.maxbo.product.export" {
		t.Errorf("type = %q", e.Type)
	}
}

func TestMessageListEmpty(t *testing.T) {
	url := messageRegistryURLFixture(t)
	// An empty registry: same shape, no collections.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"specversion":"1.0-rc2x","registryid":"intropy"}`)
	})
	tsvc := httptest.NewServer(mux)
	t.Cleanup(tsvc.Close)
	_ = url
	withRegistryURL(t, tsvc.URL)
	t.Chdir(t.TempDir())

	stdout, stderr := runMessage(t, "list", "--output", "plain")
	if !strings.Contains(stderr.String(), "no messages found") {
		t.Errorf("empty state should go to stderr, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should stay clean, got %q", stdout.String())
	}
}

func TestMessageListUnconfiguredRegistry(t *testing.T) {
	t.Setenv("INTROPY_REGISTRY_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config file
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	rootCmd.SetArgs([]string{"message", "list"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected the unconfigured-registry usage error")
	}
	for _, want := range []string{"--registry-url", "INTROPY_REGISTRY_URL", "registryUrl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should stay clean on failure, got %q", stdout.String())
	}
	_ = stderr
}

func TestMessageListUnreachableRegistry(t *testing.T) {
	withRegistryURL(t, "http://127.0.0.1:1")
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	rootCmd.SetArgs([]string{"message", "list"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected the unreachable-registry error")
	}
	if !strings.Contains(err.Error(), "GET") {
		t.Errorf("error %q should front-load the fetch that failed", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should stay clean on failure, got %q", stdout.String())
	}
	_ = stderr
}

func TestMessageShowRegistryMessage(t *testing.T) {
	withRegistryURL(t, messageRegistryURLFixture(t))
	t.Chdir(t.TempDir())

	stdout, _ := runMessage(t, "show", "io.intropy.maxbo.product.export", "--output", "plain")
	out := stdout.String()
	for _, want := range []string{
		"io.intropy.maxbo.product.export",
		"CloudEvents/1.0",
		"product-distribution-pubsub/sbt-test-product-extractor-001",
		"/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestMessageShowWorkspaceMessage(t *testing.T) {
	withRegistryURL(t, "http://127.0.0.1:1") // unreachable: a local publish must still show
	tmp := t.TempDir()
	t.Chdir(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".intropy"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, tmp+"/.intropy/scaffold.json", `{"schemaVersion":1,"template":"product-sink","owner":"o","repo":"r","version":"v1","blockKind":"extractor","values":{"appId":"product-sink","publishes":{"message":"product-exported","contract":"ProductExported"}}}`+"\n")

	stdout, _ := runMessage(t, "show", "product-exported", "--output", "plain")
	if !strings.Contains(stdout.String(), "product-exported") || !strings.Contains(stdout.String(), "ProductExported") {
		t.Errorf("show = %q", stdout.String())
	}
}

func TestMessageShowUnknownRef(t *testing.T) {
	withRegistryURL(t, messageRegistryURLFixture(t))
	t.Chdir(t.TempDir())

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resetRootIO(t, stdout, stderr)
	rootCmd.SetArgs([]string{"message", "show", "io.intropy.nosuch.message"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), "io.intropy.nosuch.message") {
		t.Errorf("error %q should name the ref", err)
	}
	_ = stdout
	_ = stderr
}

func TestMessageShowJSONOutput(t *testing.T) {
	withRegistryURL(t, messageRegistryURLFixture(t))
	t.Chdir(t.TempDir())

	stdout, _ := runMessage(t, "show", "io.intropy.maxbo.product.export", "--output", "json")
	var e MessageEntry
	if err := json.Unmarshal([]byte(stdout.String()), &e); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(e.EnvelopeMeta) == 0 {
		t.Errorf("envelope metadata missing in json: %+v", e)
	}
}
