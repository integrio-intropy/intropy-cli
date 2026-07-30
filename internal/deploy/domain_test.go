package deploy

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// Every customer integrations tree mirrors the deployment tree's shape, so the
// domain is already on disk. Verified against fluxia, lovenskiold, voice and
// entrovia, e.g. src domains/product/product-distribution ↔ gitops
// domains/product/product-distribution.
func TestDomainFromProjectPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{filepath.Join("integrations", "domains", "orders", "order-flow", "system-host"), "orders"},
		{filepath.Join("integrations", "domains", "product", "distribution", "extractor"), "product"},
		// Relative, and with the marker at the root.
		{filepath.Join("domains", "sales", "ordersync", "system-host"), "sales"},
		// No marker directory: infer nothing rather than guess.
		{filepath.Join("some", "other", "layout", "system-host"), ""},
		{filepath.Join("workspace", "system-host"), ""},
		// One segment too shallow — the parent of the domain is not "domains".
		{filepath.Join("domains", "sales", "system-host"), ""},
		{"", ""},
	} {
		if got := domainFromProjectPath(tc.path); got != tc.want {
			t.Errorf("domainFromProjectPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// The host is the system's own directory, so it is the better key; a block is
// only a fallback for the --topology case where no host was discovered.
func TestDomainFromWorkspaceLayoutPrefersTheHost(t *testing.T) {
	hostDir := filepath.Join("integrations", "domains", "orders", "order-flow", "system-host")
	blocks := []template.ScaffoldEntry{
		{Path: filepath.Join("integrations", "domains", "elsewhere", "other", "block")},
	}
	if got := domainFromWorkspaceLayout(hostDir, blocks); got != "orders" {
		t.Errorf("got %q, want orders from the host", got)
	}
	if got := domainFromWorkspaceLayout("", blocks); got != "elsewhere" {
		t.Errorf("got %q, want the block's domain as a fallback", got)
	}
	if got := domainFromWorkspaceLayout("", nil); got != "" {
		t.Errorf("got %q, want nothing to infer from", got)
	}
}

func TestResolveInitDomainFlagWins(t *testing.T) {
	var stderr bytes.Buffer
	got, err := resolveInitDomain(t.TempDir(), "explicit", "ordersync",
		filepath.Join("domains", "inferred", "ordersync", "system-host"), nil, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if got != "explicit" {
		t.Errorf("got %q, want the flag to win", got)
	}
}

func TestResolveInitDomainFromWorkspace(t *testing.T) {
	var stderr bytes.Buffer
	hostDir := filepath.Join("integrations", "domains", "orders", "order-flow", "system-host")

	got, err := resolveInitDomain(t.TempDir(), "", "order-flow", hostDir, nil, &stderr)
	if err != nil {
		t.Fatalf("the workspace layout should supply the domain: %v", err)
	}
	if got != "orders" {
		t.Errorf("got %q, want orders", got)
	}
	// Said out loud, because it silently decides where the tree is written.
	if !strings.Contains(stderr.String(), "from the workspace layout") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// Moving a system between domains is a deliberate act, not something a directory
// name should trigger.
func TestResolveInitDomainTreeWinsOverWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"domains/orders/order-flow/order-extractor/component.yaml": "schemaVersion: 1\n",
	})

	var stderr bytes.Buffer
	hostDir := filepath.Join("integrations", "domains", "logistics", "order-flow", "system-host")
	got, err := resolveInitDomain(root, "", "order-flow", hostDir, nil, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if got != "orders" {
		t.Errorf("got %q, want the domain the repository already uses", got)
	}
	for _, want := range []string{"logistics", "keeping orders", "--domain"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("warning missing %q: %q", want, stderr.String())
		}
	}
}

func TestResolveInitDomainNeitherSourceIsAnError(t *testing.T) {
	var stderr bytes.Buffer
	_, err := resolveInitDomain(t.TempDir(), "", "ordersync",
		filepath.Join("some", "other", "layout", "system-host"), nil, &stderr)
	if err == nil {
		t.Fatal("expected an error with nothing to infer from")
	}
	for _, want := range []string{"--domain is required", "domains/<domain>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestResolveInitDomainAmbiguousInTree(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"domains/orders/order-flow/a/component.yaml":    "schemaVersion: 1\n",
		"domains/logistics/order-flow/b/component.yaml": "schemaVersion: 1\n",
	})

	var stderr bytes.Buffer
	_, err := resolveInitDomain(root, "", "order-flow", "", nil, &stderr)
	if err == nil {
		t.Fatal("expected an error when two domains hold the system")
	}
	for _, want := range []string{"logistics", "orders"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
