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

// newLocalFixtureWith serves the given template entries over the GitHubBaseURL
// seam and lays out a workspace with a topology file. It mirrors
// newInitFixtureWith without its GitOps machinery: a local render never
// opens a repository.
func newLocalFixtureWith(t *testing.T, entries map[string]string) localFixture {
	t.Helper()
	f := newLocalFixture(t)
	f.srv.Close()
	f.srv = localLibraryServer(t, entries)
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
	writeDeployValues(t, f.sourceDir, bothBound)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.Runner = nil // the real kustomize binary via applyDefaults
	if err := Init(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	t.Logf("rendered %d bytes", stdout.Len())

	// The output must be multi-document YAML kubectl will accept.
	apply := exec.Command("kubectl", "apply", "--dry-run=client", "-f", "-")
	apply.Stdin = &stdout
	if out, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("kubectl apply --dry-run=client: %v\n%s", err, out)
	}

	// The pinned image convention: every Deployment image carries a tag, and
	// component.yaml never reaches the stream.
	text := stdout.String()
	if strings.Contains(text, "schemaVersion") {
		t.Errorf("component.yaml was rendered into the manifest stream")
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
	writeDeployValues(t, workspace, bothBound)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	opts.Runner = nil
	if err := Init(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	want := filepath.Join(workspace, "domains", "x", "distribution", "system-host")
	if *called != want {
		t.Errorf("graph verb ran on %q, want %q", *called, want)
	}
	if stdout.Len() == 0 {
		t.Error("no manifests on stdout")
	}
}
