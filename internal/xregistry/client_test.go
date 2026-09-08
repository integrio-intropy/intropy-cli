package xregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// exportFixture is the /export document of a registry shaped like the
// Intropy service: one message group with one message, one producer
// endpoint carrying the group on one channel, and one schema group whose
// resource has the default version the message's dataschemaxid points at.
func exportFixture() map[string]any {
	return map[string]any{
		"specversion": "1.0-rc2x",
		"registryid":  "intropy",
		"messagegroups": map[string]any{
			"io.intropy.maxbo.product": map[string]any{
				"messagegroupid": "io.intropy.maxbo.product",
				"name":           "Maxbo product distribution",
				"messages": map[string]any{
					"io.intropy.maxbo.product.export": map[string]any{
						"messageid": "io.intropy.maxbo.product.export",
						"versions": map[string]any{
							"1": map[string]any{
								"versionid": "1",
								"isdefault": true,
								"envelope":  "CloudEvents/1.0",
								"envelopemetadata": map[string]any{
									"type":   map[string]any{"value": "io.intropy.maxbo.product.export", "required": true},
									"source": map[string]any{"value": "urn:maxbo:perfion", "required": true},
								},
								"dataschemaxid": "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
							},
						},
					},
				},
			},
		},
		"endpoints": map[string]any{
			"perfion-extractor": map[string]any{
				"endpointid": "perfion-extractor",
				"usage":      []string{"producer"},
				"channel":    "product-distribution-pubsub/sbt-test-product-extractor-001",
				"messagegroups": []string{
					"/messagegroups/io.intropy.maxbo.product",
				},
			},
			"maxbo-loader": map[string]any{
				"endpointid": "maxbo-loader",
				"usage":      []string{"subscriber"},
				"channel":    "product-distribution-pubsub/sbt-test-product-loader-001",
				"messagegroups": []string{
					"/messagegroups/io.intropy.maxbo.product",
				},
			},
		},
		"schemagroups": map[string]any{
			"io.intropy.maxbo.product": map[string]any{
				"schemagroupid": "io.intropy.maxbo.product",
				"schemas": map[string]any{
					"product-export.v1": map[string]any{
						"schemaid": "product-export.v1",
						"versions": map[string]any{
							"1": map[string]any{
								"versionid": "1",
								"isdefault": true,
								"xid":       "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
							},
							"2": map[string]any{
								"versionid": "2",
								"isdefault": false,
								"xid":       "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/2",
							},
						},
					},
				},
			},
		},
	}
}

// serveExport builds a client against an httptest server answering /export
// with the fixture.
func serveExport(t *testing.T, doc map[string]any) (*Client, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, doc)
	})
	tsvc := httptest.NewServer(mux)
	t.Cleanup(tsvc.Close)
	c, err := New(tsvc.URL, WithUserAgent("test"))
	if err != nil {
		t.Fatal(err)
	}
	return c, mux
}

func writeJSON(t *testing.T, w http.ResponseWriter, doc any) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func TestResolveHappyPath(t *testing.T) {
	c, _ := serveExport(t, exportFixture())
	res, err := c.Resolve(context.Background(), "io.intropy.maxbo.product.export")
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "io.intropy.maxbo.product.export" {
		t.Errorf("Message = %q", res.Message)
	}
	if res.Type != "io.intropy.maxbo.product.export" {
		t.Errorf("Type = %q, want the envelopemetadata.type value", res.Type)
	}
	if res.Group != "io.intropy.maxbo.product" {
		t.Errorf("Group = %q", res.Group)
	}
	if len(res.Channels) != 1 {
		t.Fatalf("got %d channels, want 1: %+v", len(res.Channels), res.Channels)
	}
	ch := res.Channels[0]
	if ch.Pubsub != "product-distribution-pubsub" || ch.Topic != "sbt-test-product-extractor-001" {
		t.Errorf("channel = %+v, want the producer endpoint's pubsub/topic", ch)
	}
	if ch.Endpoint != "perfion-extractor" {
		t.Errorf("channel endpoint = %q, want perfion-extractor", ch.Endpoint)
	}
	if res.DataSchema != "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1" {
		t.Errorf("DataSchema = %q", res.DataSchema)
	}
	// The pin is the default version, absolute — not the logical resource
	// and not a newer version.
	want := c.BaseURL() + "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1"
	if res.DataSchemaURL != want {
		t.Errorf("DataSchemaURL = %q, want %q", res.DataSchemaURL, want)
	}
}

func TestResolveFromExportMessageAttributesComeFromTheDefaultVersion(t *testing.T) {
	// The doc view does not repeat the default version's attributes on the
	// message resource; a reader that takes envelopemetadata from the
	// resource sees nothing.
	doc := exportFixture()
	c, _ := serveExport(t, doc)
	res, err := c.Resolve(context.Background(), "io.intropy.maxbo.product.export")
	if err != nil {
		t.Fatal(err)
	}
	if res.Type == "" {
		t.Error("Type is empty — attributes must be read from the default version, not the message resource")
	}
	if res.DataSchema == "" {
		t.Error("DataSchema is empty — attributes must be read from the default version, not the message resource")
	}
}

func TestResolveXidReference(t *testing.T) {
	c, _ := serveExport(t, exportFixture())
	res, err := c.Resolve(context.Background(), "/messagegroups/io.intropy.maxbo.product/messages/io.intropy.maxbo.product.export")
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "io.intropy.maxbo.product.export" {
		t.Errorf("Message = %q, want the xid to resolve to the same message", res.Message)
	}
}

func TestResolveMessageNotFound(t *testing.T) {
	c, _ := serveExport(t, exportFixture())
	_, err := c.Resolve(context.Background(), "io.intropy.nosuch.message")
	var notFound *MessageNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want MessageNotFoundError", err)
	}
	if !strings.Contains(err.Error(), "io.intropy.nosuch.message") {
		t.Errorf("error %q should name the ref", err)
	}
}

func TestResolveNoProducerEndpoint(t *testing.T) {
	doc := exportFixture()
	delete(doc["endpoints"].(map[string]any), "perfion-extractor")
	c, _ := serveExport(t, doc)
	res, err := c.Resolve(context.Background(), "io.intropy.maxbo.product.export")
	if err != nil {
		t.Fatal(err)
	}
	_, chErr := res.SubscribeChannel()
	var noProd *NoProducerError
	if !errors.As(chErr, &noProd) {
		t.Fatalf("err = %v, want NoProducerError", chErr)
	}
	if !strings.Contains(chErr.Error(), "io.intropy.maxbo.product") {
		t.Errorf("error %q should name the group", chErr)
	}
}

func TestResolveAmbiguousProducers(t *testing.T) {
	doc := exportFixture()
	eps := doc["endpoints"].(map[string]any)
	eps["backup-extractor"] = map[string]any{
		"endpointid":    "backup-extractor",
		"usage":         []string{"producer"},
		"channel":       "other-pubsub/other-topic",
		"messagegroups": []string{"/messagegroups/io.intropy.maxbo.product"},
	}
	c, _ := serveExport(t, doc)
	res, err := c.Resolve(context.Background(), "io.intropy.maxbo.product.export")
	if err != nil {
		t.Fatal(err)
	}
	_, chErr := res.SubscribeChannel()
	var amb *AmbiguousProducerError
	if !errors.As(chErr, &amb) {
		t.Fatalf("err = %v, want AmbiguousProducerError", chErr)
	}
	for _, want := range []string{"sbt-test-product-extractor-001", "other-topic"} {
		if !strings.Contains(chErr.Error(), want) {
			t.Errorf("error %q should list candidate channel %q", chErr, want)
		}
	}
}

func TestResolveMalformedChannel(t *testing.T) {
	for _, channel := range []string{"no-slash", "/empty-pubsub", "empty-topic/"} {
		t.Run(channel, func(t *testing.T) {
			doc := exportFixture()
			doc["endpoints"].(map[string]any)["perfion-extractor"].(map[string]any)["channel"] = channel
			c, _ := serveExport(t, doc)
			_, err := c.Resolve(context.Background(), "io.intropy.maxbo.product.export")
			var fmtErr *ChannelFormatError
			if !errors.As(err, &fmtErr) {
				t.Fatalf("err = %v, want ChannelFormatError", err)
			}
			if !strings.Contains(err.Error(), "perfion-extractor") {
				t.Errorf("error %q should name the endpoint", err)
			}
			if !strings.Contains(err.Error(), channel) {
				t.Errorf("error %q should name the channel", err)
			}
		})
	}
}

func TestResolveSchemaNotFound(t *testing.T) {
	doc := exportFixture()
	delete(doc["schemagroups"].(map[string]any), "io.intropy.maxbo.product")
	c, _ := serveExport(t, doc)
	_, err := c.Resolve(context.Background(), "io.intropy.maxbo.product.export")
	var schemaErr *SchemaNotFoundError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("err = %v, want SchemaNotFoundError", err)
	}
	if !strings.Contains(err.Error(), "product-export.v1") {
		t.Errorf("error %q should name the dataschemaxid", err)
	}
}

func TestRegistryErrorBodySurfaced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(t, w, map[string]any{"code": "unavailable", "detail": "registry warming up", "status": 503})
	})
	tsvc := httptest.NewServer(mux)
	t.Cleanup(tsvc.Close)
	c, err := New(tsvc.URL, WithUserAgent("test"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Export(context.Background())
	if err == nil {
		t.Fatal("expected an error for the registry response")
	}
	for _, want := range []string{"unavailable", "registry warming up"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should surface the registry body's %q", err, want)
		}
	}
}

func TestUnreachableHostIsWrappedNotPanicked(t *testing.T) {
	c, err := New("http://127.0.0.1:1", WithUserAgent("test"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Export(context.Background())
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if !strings.Contains(err.Error(), "fetch xRegistry export") || !strings.Contains(err.Error(), "GET") {
		t.Errorf("error %q should wrap the operation, not panic", err)
	}
}

func TestNewDefaultHTTPClientHasTimeout(t *testing.T) {
	c, err := New("https://registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient.Timeout != defaultHTTPTimeout {
		t.Errorf("timeout = %v, want %v", c.httpClient.Timeout, defaultHTTPTimeout)
	}

	custom := &http.Client{}
	c, err = New("https://registry.example.com", WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient != custom {
		t.Error("WithHTTPClient should keep the caller-provided client")
	}
}

func TestNewRejectsNonAbsoluteBaseURL(t *testing.T) {
	if _, err := New("not-a-url"); err == nil {
		t.Error("New should reject a relative base URL")
	}
}
