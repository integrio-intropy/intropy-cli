package template

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	srv := newTemplateServer(t, "v1.2.3")
	defer srv.Close()

	got, err := Describe(context.Background(), DescribeOptions{
		Template:      "test-template",
		Version:       "v1.2.3",
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got.Template != "test-template" {
		t.Errorf("Template = %q", got.Template)
	}
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q", got.Version)
	}
	if got.Parameters == nil {
		t.Fatal("Parameters is nil")
	}
	if got.Parameters["type"] != "object" {
		t.Errorf("parameters.type = %v", got.Parameters["type"])
	}
	props, _ := got.Parameters["properties"].(map[string]any)
	if _, ok := props["integrationName"]; !ok {
		t.Errorf("parameters.properties.integrationName missing")
	}
}

func TestDescribeJSONStable(t *testing.T) {
	srv := newTemplateServer(t, "v1")
	defer srv.Close()

	got, err := Describe(context.Background(), DescribeOptions{
		Template:      "test-template",
		Version:       "v1",
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	// Pin the public field names. Adding new fields is fine; renaming/removing
	// these breaks downstream Backstage consumers.
	for _, key := range []string{
		`"template":`, `"owner":`, `"repo":`, `"version":`,
		`"parameters":`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("missing key %s in JSON: %s", key, string(data))
		}
	}
}

func TestDescribeFormatTextPreservesDeclarationOrder(t *testing.T) {
	srv := newTemplateServer(t, "v1")
	defer srv.Close()

	got, err := Describe(context.Background(), DescribeOptions{
		Template:      "test-template",
		Version:       "v1",
		HTTP:          srv.Client(),
		Owner:         "o",
		Repo:          "r",
		GitHubBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	got.FormatText(&buf)
	out := buf.String()
	iName := strings.Index(out, "integrationName")
	iNamespace := strings.Index(out, "namespace")
	if iName < 0 || iNamespace < 0 {
		t.Fatalf("expected both fields in output: %s", out)
	}
	if iName > iNamespace {
		t.Errorf("expected integrationName before namespace (YAML declaration order), got:\n%s", out)
	}
}
