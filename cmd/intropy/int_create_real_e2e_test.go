//go:build e2e

package main

// The full command intake against the real template library (WS-1.3):
// --publishes / --subscribe resolve through the registry fixture and land
// block-shaped records in the real extractor/loader scaffold records, the
// same flow Gate G1 runs. The template library comes from a local checkout
// (INTROPY_TEMPLATES_DIR) through the same fetch seams production uses; see
// internal/template's real-manifest tests for the gate alone.
//
// Run with:
//
//	INTROPY_TEMPLATES_DIR=</path/to/intropy-templates> go test -tags e2e ./cmd/intropy/ -run RealLibrary -v

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// realTemplatesSource mirrors the helper in internal/template: mirror the
// checkout's current branch into a throwaway repo and pin a tag-shaped name
// (a slash in the pin would collide with the cache's temp-dir naming).
func realTemplatesSource(t *testing.T) (template.SourceOptions, string) {
	const pin = "message-first-e2e"
	t.Helper()
	dir := os.Getenv("INTROPY_TEMPLATES_DIR")
	if dir == "" {
		t.Skip("INTROPY_TEMPLATES_DIR not set")
	}
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
	mirror := filepath.Join(t.TempDir(), "templates-mirror")
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", branch, abs, mirror).CombinedOutput(); err != nil {
		t.Fatalf("mirror the checkout at %s: %v\n%s", branch, err, out)
	}
	if out, err := exec.Command("git", "-C", mirror, "tag", pin, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("pin the mirrored checkout: %v\n%s", err, out)
	}
	return template.SourceOptions{
		RepoURL:   "file://" + mirror,
		CacheRoot: t.TempDir(),
	}, pin
}

// The producer side of the gate command sequence, against the real extractor
// manifest: registry resolution feeds --publishes, and the scaffold record
// carries the full block.
func TestIntCreateRealLibraryPublishesExtractor(t *testing.T) {
	registry := messageRegistryFixture(t)
	t.Setenv("INTROPY_REGISTRY_URL", registry.URL)
	src, version := realTemplatesSource(t)

	outDir := filepath.Join(t.TempDir(), "order-sweep")
	block, err := resolvePublishesBlock(context.Background(), "io.intropy.maxbo.product.export", "")
	if err != nil {
		t.Fatalf("resolvePublishesBlock: %v", err)
	}
	if block.Topic != "sbt-test-product-extractor-001" {
		t.Fatalf("resolved producing channel = %s/%s, want the fixture registry's", block.Pubsub, block.Topic)
	}

	err = template.Create(context.Background(), template.CreateOptions{
		Template:  "extractor",
		OutputDir: outDir,
		Version:   version,
		NoInput:   true,
		Stderr:    io.Discard,
		Source:    src,
		SetValues: map[string]any{
			"name":         "OrderSweep",
			"organization": "Maxbo",
			"topic":        block.Topic,
			"contract":     "Order",
		},
		Publishes: block,
	})
	if err != nil {
		t.Fatal(err)
	}

	record, err := template.LoadScaffold(filepath.Join(outDir, template.ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	entry := template.ScaffoldEntry{Path: outDir, Scaffold: *record}
	got, err := template.ReadPublishesBlock(entry)
	if err != nil {
		t.Fatalf("scaffold record is not block-shaped: %v (values: %v)", err, record.Values)
	}
	if *got != *block {
		t.Errorf("record publishes block = %+v, want the resolved %+v", *got, *block)
	}
	if got, ok := record.Values[template.KeyMessage].(string); !ok || got != block.Message {
		t.Errorf("values.message = %v, want the resolved message", record.Values[template.KeyMessage])
	}
}

// The consumer side: --subscribe resolves the producing channel and the
// loader's scaffold record carries the full block.
func TestIntCreateRealLibrarySubscribeLoader(t *testing.T) {
	registry := messageRegistryFixture(t)
	t.Setenv("INTROPY_REGISTRY_URL", registry.URL)
	src, version := realTemplatesSource(t)

	outDir := filepath.Join(t.TempDir(), "order-file-loader")
	var stderr bytes.Buffer
	block, err := resolveSubscribeBlock(context.Background(), "io.intropy.maxbo.product.export", "", true, &stderr)
	if err != nil {
		t.Fatalf("resolveSubscribeBlock: %v\nstderr: %s", err, stderr.String())
	}

	err = template.Create(context.Background(), template.CreateOptions{
		Template:  "loader",
		OutputDir: outDir,
		Version:   version,
		NoInput:   true,
		Stderr:    io.Discard,
		Source:    src,
		SetValues: map[string]any{
			"name":         "OrderFileLoader",
			"organization": "Maxbo",
			"topic":        block.Topic,
			"contract":     "Order",
		},
		Subscribe: block,
	})
	if err != nil {
		t.Fatal(err)
	}

	record, err := template.LoadScaffold(filepath.Join(outDir, template.ScaffoldRelPath))
	if err != nil {
		t.Fatal(err)
	}
	entry := template.ScaffoldEntry{Path: outDir, Scaffold: *record}
	got, err := template.ReadSubscribeBlock(entry)
	if err != nil {
		t.Fatalf("scaffold record is not block-shaped: %v (values: %v)", err, record.Values)
	}
	if *got != *block {
		t.Errorf("record subscribe block = %+v, want the resolved %+v", *got, *block)
	}
}
