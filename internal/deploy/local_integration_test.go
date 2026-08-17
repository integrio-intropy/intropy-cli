//go:build integration

package deploy

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireKubectl gates the optional apply-time validation, mirroring
// requireKustomize: a second tool dependency, skipped rather than failed.
func requireKubectl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl is not installed")
	}
}

// newLocalFixtureWith builds the given template entries as a git-backed
// library and lays out a workspace with a topology file. It mirrors
// newInitFixtureWith without its GitOps machinery: a local render never
// opens a repository.
func newLocalFixtureWith(t *testing.T, entries map[string]string) localFixture {
	t.Helper()
	f := newLocalFixture(t)
	f.lib = localLibrary(t, entries)
	f.source = f.lib.Source(t)
	return f
}

// The real templates render the local render end to end: both topology seams,
// the local overlay from the fetched library, and a kustomize build whose
// output kubectl accepts. Skipped until the templates repo ships the local
// overlay and the fixture catalog — the skip message names exactly what is
// missing.
//
// Run with INTROPY_TEMPLATES_DIR set and -count=1; see localTemplatesRoot.
func TestLocalTemplatesRenderForLocalCluster(t *testing.T) {
	requireKustomize(t)
	requireKubectl(t)
	root := localTemplatesRoot(t)

	entries := localTemplateEntries(t, root)
	if !strings.Contains(entries["deploy-component/template.yaml"], "fixtures") {
		t.Skip("the templates checkout has no spec.local.fixtures catalog yet — the local overlay lands in the templates repo first")
	}

	f := newLocalFixtureWith(t, entries)

	var stdout, stderr bytes.Buffer
	opts := f.renderOptions(&stderr)
	built, err := RenderManifests(context.Background(), opts)
	if err != nil {
		t.Fatalf("RenderManifests: %v\nstderr: %s", err, stderr.String())
	}
	stdout.Write(built)
	t.Logf("rendered %d bytes", stdout.Len())

	// The output must be multi-document YAML kubectl will accept. Read the
	// buffer before wiring it to exec.Stdin — the copy drains it.
	text := stdout.String()
	apply := exec.Command("kubectl", "apply", "--dry-run=client", "-f", "-")
	apply.Stdin = &stdout
	if out, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("kubectl apply --dry-run=client: %v\n%s", err, out)
	}

	// The pinned image convention: every Deployment image resolves to
	// local/<component>:dev — the tag the k3s setup scripts import — and
	// component.yaml never reaches the stream. The CLI's tag check already
	// ran; this pins the shape itself against template drift.
	if strings.Contains(text, "schemaVersion") {
		t.Errorf("component.yaml was rendered into the manifest stream")
	}
	if !strings.Contains(text, "image: local/erp-loader:dev") {
		t.Errorf("no Deployment image carries the pinned local/<component>:dev shape; rendered %d bytes", len(text))
	}
}

// The graph-verb seam against the real templates: no topology file, so host
// discovery and runGraph supply the record.
func TestLocalTemplatesDiscoverTheHost(t *testing.T) {
	requireKustomize(t)
	root := localTemplatesRoot(t)

	entries := localTemplateEntries(t, root)
	if !strings.Contains(entries["deploy-component/template.yaml"], "fixtures") {
		t.Skip("the templates checkout has no spec.local.fixtures catalog yet — the local overlay lands in the templates repo first")
	}

	f := newLocalFixtureWith(t, entries)

	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "distribution")
	called := stubRunGraph(t, localTopologyRecord)

	var stderr bytes.Buffer
	opts := f.renderOptions(&stderr)
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	built, err := RenderManifests(context.Background(), opts)
	if err != nil {
		t.Fatalf("RenderManifests: %v\nstderr: %s", err, stderr.String())
	}
	want := filepath.Join(workspace, "domains", "x", "distribution", "system-host")
	if *called != want {
		t.Errorf("graph verb ran on %q, want %q", *called, want)
	}
	if len(built) == 0 {
		t.Error("no rendered manifests")
	}
}
