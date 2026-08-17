package system

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/template/templatetest"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// writeHost scaffolds a system host the way Create leaves it: a record
// whose values carry the full facts payload plus the template-derived
// names, and a marker file simulating a rendered declaration file.
func writeHost(t *testing.T, dir string, values map[string]any) {
	t.Helper()
	if values == nil {
		values = hostValues("order-flow", []any{
			componentEntry("order-extractor", template.BlockKindExtractor, "orders", "order-extractor-source"),
			componentEntry("order-loader", template.BlockKindLoader, "orders", "order-loader-destination"),
		})
	}
	err := template.WriteScaffold(dir, template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "system-host",
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		Role:          template.RoleSystemHost,
		Values:        values,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// hostValues mirrors the payload buildPayload produces plus the derived
// values spec.values adds at create time: what PrepareCreate re-derives on
// update, so a no-drift render is byte-identical.
func hostValues(name string, components []any) map[string]any {
	return map[string]any{
		"name":        name,
		"projectName": "OrderFlow",
		"systemClass": "OrderFlowSystem",
		"topics": []any{
			map[string]any{"pubsub": "pubsub", "name": "orders", "contract": "Order"},
		},
		"ports": []any{
			map[string]any{"name": "order-extractor-source"},
			map[string]any{"name": "order-loader-destination"},
		},
		"components": components,
	}
}

// componentEntry is one values.components element as the host record
// stores it.
func componentEntry(appID, kind, topic, port string) map[string]any {
	return map[string]any{
		"appId": appID,
		"kind":  kind,
		"topic": map[string]any{"pubsub": "pubsub", "name": topic},
		"port":  port,
	}
}

// updateWorkspace is the update fixture: the tutorial workspace plus a
// host declaring both components, with the declaration files rendered from
// the recorded values on disk — the state sys create leaves behind, which
// the update's baseline comparison reads.
func updateWorkspace(t *testing.T) (ws, hostDir string) {
	t.Helper()
	ws = writeWorkspace(t)
	hostDir = filepath.Join(ws, "order-flow")
	values := hostValues("order-flow", []any{
		componentEntry("order-extractor", template.BlockKindExtractor, "orders", "order-extractor-source"),
		componentEntry("order-loader", template.BlockKindLoader, "orders", "order-loader-destination"),
	})
	writeHost(t, hostDir, values)
	renderFixtureHost(t, hostDir, values)
	return ws, hostDir
}

// renderFixtureHost writes the files the system-host fixture template
// renders from values, resolved the way PrepareCreate resolves them (the
// spec.values derivations are already in the recorded values).
func renderFixtureHost(t *testing.T, hostDir string, values map[string]any) {
	t.Helper()
	src := t.TempDir()
	for name, content := range systemHostFiles() {
		const prefix = "system-host/skeleton/"
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		path := filepath.Join(src, filepath.FromSlash(strings.TrimPrefix(name, prefix)))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := template.Render(src, hostDir, values); err != nil {
		t.Fatalf("render fixture host: %v", err)
	}
}

func runUpdate(t *testing.T, lib *templatetest.Library, opts UpdateOptions) (stdout, stderr bytes.Buffer, err error) {
	t.Helper()
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	opts.Source = lib.Source(t)
	err = Update(context.Background(), opts)
	return stdout, stderr, err
}

func TestUpdateFoldsOrphanIntoHost(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	writeBlock(t, filepath.Join(ws, "billing-sync"), template.BlockKindTransactional, "billing-sync")

	// The transactional fixture needs ports in its record; writeBlock only
	// writes topic-block values, so rewrite it with from/to.
	err := template.WriteScaffold(filepath.Join(ws, "billing-sync"), template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      template.BlockKindTransactional,
		Owner:         "o",
		Repo:          "r",
		Version:       "v1",
		BlockKind:     template.BlockKindTransactional,
		Values:        map[string]any{"appId": "billing-sync", "fromPort": "erp", "toPort": "billing"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err != nil {
		t.Fatalf("Update: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "updating "+hostDir+": adding components billing-sync") {
		t.Errorf("stderr missing progress line:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "added 1 component(s)") {
		t.Errorf("stderr missing completion line:\n%s", stderr.String())
	}

	// The system definition now wires the orphan, and its ports were added
	// to the shared declarations without a conflict.
	systemClass := readFile(t, filepath.Join(hostDir, "OrderFlowSystem.cs"))
	if !strings.Contains(systemClass, `builder.AddTransactionalIntegration("billing-sync")`) {
		t.Errorf("OrderFlowSystem.cs does not declare billing-sync:\n%s", systemClass)
	}
	if !strings.Contains(systemClass, "Ports.Erp") || !strings.Contains(systemClass, "Ports.Billing") {
		t.Errorf("OrderFlowSystem.cs does not wire the new ports:\n%s", systemClass)
	}
	portsCS := readFile(t, filepath.Join(hostDir, "Ports.cs"))
	if !strings.Contains(portsCS, `PortRef.Define("erp")`) || !strings.Contains(portsCS, `PortRef.Define("billing")`) {
		t.Errorf("Ports.cs does not declare the new ports:\n%s", portsCS)
	}

	// The record was rewritten with the merged values.
	rec, err := template.LoadScaffold(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	components, _ := rec.Values["components"].([]any)
	if len(components) != 3 {
		t.Fatalf("record declares %d components, want 3", len(components))
	}
	ports, _ := rec.Values["ports"].([]any)
	if len(ports) != 4 {
		t.Errorf("record declares %d ports, want 4", len(ports))
	}

	// Idempotence: a second run is a no-op.
	_, stderr2, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if !strings.Contains(stderr2.String(), "no orphaned components found") {
		t.Errorf("second run stderr = %q, want the empty state", stderr2.String())
	}
}

func TestUpdateNoOrphansIsNoOp(t *testing.T) {
	lib, hits := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	marker := filepath.Join(hostDir, "marker.txt")
	if err := os.WriteFile(marker, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !strings.Contains(stderr.String(), "no orphaned components found") {
		t.Errorf("stderr = %q, want the empty state", stderr.String())
	}
	if hits.Load() != 0 {
		t.Errorf("no-orphan update hit the network %d times", hits.Load())
	}
	if got := readFile(t, marker); got != "untouched\n" {
		t.Errorf("marker.txt modified: %q", got)
	}
}

func TestUpdatePreservesReverseOrphan(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	// The loader's scaffold disappears after the host declared it.
	if err := os.RemoveAll(filepath.Join(ws, "order-loader")); err != nil {
		t.Fatal(err)
	}
	writeBlock(t, filepath.Join(ws, "returns-extractor"), template.BlockKindExtractor, "returns-extractor")

	_, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err != nil {
		t.Fatalf("Update: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "note: component order-loader declared but no scaffold found") {
		t.Errorf("stderr missing reverse-orphan note:\n%s", stderr.String())
	}

	rec, err := template.LoadScaffold(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	components, _ := rec.Values["components"].([]any)
	appIDs := map[string]bool{}
	for _, c := range components {
		m := c.(map[string]any)
		appIDs[m["appId"].(string)] = true
	}
	if !appIDs["order-loader"] {
		t.Errorf("order-loader was dropped from the record: %v", appIDs)
	}
	if !appIDs["returns-extractor"] {
		t.Errorf("returns-extractor was not added to the record: %v", appIDs)
	}
}

func TestUpdateRendersWithRecordPin(t *testing.T) {
	// The record pins v9; only a fetch of v9 can succeed, so a run that
	// silently resolved latest fails loudly.
	lib, _ := newSystemHostLibrary(t, "v9", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	values := hostValues("order-flow", []any{
		componentEntry("order-extractor", template.BlockKindExtractor, "orders", "order-extractor-source"),
		componentEntry("order-loader", template.BlockKindLoader, "orders", "order-loader-destination"),
	})
	writeHost(t, hostDir, values)
	rec, err := template.LoadScaffold(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	rec.Version = "v9"
	if err := template.WriteScaffold(hostDir, *rec); err != nil {
		t.Fatal(err)
	}

	writeBlock(t, filepath.Join(ws, "returns-extractor"), template.BlockKindExtractor, "returns-extractor")

	if _, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws}); err != nil {
		t.Fatalf("Update: %v\nstderr: %s", err, stderr.String())
	}

	rec, err = template.LoadScaffold(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v9" {
		t.Errorf("record version = %q, want the pinned v9", rec.Version)
	}
}

func TestUpdateDryRunWritesNothing(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	writeBlock(t, filepath.Join(ws, "returns-extractor"), template.BlockKindExtractor, "returns-extractor")

	recordBefore, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	systemBefore := readFile(t, filepath.Join(hostDir, "OrderFlowSystem.cs"))

	stdout, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws, DryRun: true, OutputJSON: "-"})
	if err != nil {
		t.Fatalf("Update: %v\nstderr: %s", err, stderr.String())
	}

	// The plan names the orphan and the files that would change: the
	// system class matches the baseline render, so folding the orphan in
	// is an update, not a conflict.
	var result UpdateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode UpdateResult: %v\n%s", err, stdout.String())
	}
	if len(result.Added) != 1 || result.Added[0] != "returns-extractor" {
		t.Errorf("added = %v, want [returns-extractor]", result.Added)
	}
	if !result.DryRun {
		t.Errorf("dryRun = false in result")
	}
	outcomes := map[string]template.FileOutcomeKind{}
	for _, f := range result.Files {
		outcomes[f.Path] = f.Outcome
	}
	if outcomes["OrderFlowSystem.cs"] != template.OutcomeUpdated {
		t.Errorf("OrderFlowSystem.cs outcome = %q, want updated", outcomes["OrderFlowSystem.cs"])
	}

	// Neither the record nor any declaration file changed on disk.
	recordAfter, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordBefore, recordAfter) {
		t.Errorf("dry-run rewrote the scaffold record")
	}
	if got := readFile(t, filepath.Join(hostDir, "OrderFlowSystem.cs")); got != systemBefore {
		t.Errorf("dry-run modified OrderFlowSystem.cs")
	}
}

func TestUpdateConflictRefusesAndKeepsBaseline(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	writeBlock(t, filepath.Join(ws, "returns-extractor"), template.BlockKindExtractor, "returns-extractor")

	// A hand-edited declaration file conflicts with the re-render.
	conflicted := filepath.Join(hostDir, "Ports.cs")
	if err := os.WriteFile(conflicted, []byte("// hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recordBefore, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err == nil {
		t.Fatal("Update succeeded despite a conflicting file")
	}
	if !strings.Contains(err.Error(), "Ports.cs") {
		t.Errorf("error does not name the conflicting file: %v", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error does not name the escape hatch: %v", err)
	}

	// The conflicted file and the record both survive untouched.
	if got := readFile(t, conflicted); got != "// hand-edited\n" {
		t.Errorf("conflict overwrote Ports.cs: %q", got)
	}
	recordAfter, err := os.ReadFile(filepath.Join(hostDir, filepath.FromSlash(template.ScaffoldRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordBefore, recordAfter) {
		t.Errorf("record was rewritten despite the conflict — a rerun would miss the orphan")
	}
}

func TestUpdateForceOverwritesConflict(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	writeBlock(t, filepath.Join(ws, "returns-extractor"), template.BlockKindExtractor, "returns-extractor")
	conflicted := filepath.Join(hostDir, "Ports.cs")
	if err := os.WriteFile(conflicted, []byte("// hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws, Force: true}); err != nil {
		t.Fatalf("Update --force: %v\nstderr: %s", err, stderr.String())
	}
	got := readFile(t, conflicted)
	if !strings.Contains(got, "PortRef.Define") {
		t.Errorf("force did not overwrite Ports.cs:\n%s", got)
	}
}

func TestUpdateErrorsWithoutHost(t *testing.T) {
	lib, hits := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws := t.TempDir()
	writeBlock(t, filepath.Join(ws, "order-extractor"), template.BlockKindExtractor, "order-extractor")

	_, _, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err == nil {
		t.Fatal("Update succeeded without a host")
	}
	if !strings.Contains(err.Error(), "sys create") {
		t.Errorf("error does not mention sys create: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("host-less update hit the network %d times", hits.Load())
	}
}

func TestUpdateErrorsInsideHostDir(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	_, hostDir := updateWorkspace(t)
	_, _, err := runUpdate(t, lib, UpdateOptions{StartDir: hostDir})
	if err == nil {
		t.Fatal("Update succeeded from inside the host directory")
	}
	if !strings.Contains(err.Error(), "workspace root") {
		t.Errorf("error does not direct the user to the workspace root: %v", err)
	}
}

func TestUpdateErrorsWithTwoHosts(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, _ := updateWorkspace(t)
	writeHost(t, filepath.Join(ws, "second-host"), nil)

	_, _, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err == nil {
		t.Fatal("Update succeeded with two hosts")
	}
	if !strings.Contains(err.Error(), "order-flow") || !strings.Contains(err.Error(), "second-host") {
		t.Errorf("error does not name both hosts: %v", err)
	}
}

func TestUpdateErrorsOnMalformedComponents(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	values := hostValues("order-flow", []any{
		map[string]any{"kind": "extractor"}, // no appId
	})
	writeHost(t, hostDir, values)

	_, _, err := runUpdate(t, lib, UpdateOptions{StartDir: ws})
	if err == nil {
		t.Fatal("Update succeeded on a malformed record")
	}
	if !strings.Contains(err.Error(), template.ScaffoldRelPath) {
		t.Errorf("error does not name the record path: %v", err)
	}
}

func TestUpdateJSONOutput(t *testing.T) {
	lib, _ := newSystemHostLibrary(t, "v1", systemHostFiles())

	ws, hostDir := updateWorkspace(t)
	writeBlock(t, filepath.Join(ws, "returns-extractor"), template.BlockKindExtractor, "returns-extractor")

	stdout, stderr, err := runUpdate(t, lib, UpdateOptions{StartDir: ws, OutputJSON: "-"})
	if err != nil {
		t.Fatalf("Update: %v\nstderr: %s", err, stderr.String())
	}

	var result UpdateResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode UpdateResult: %v\n%s", err, stdout.String())
	}
	if result.System != "order-flow" {
		t.Errorf("system = %q, want order-flow", result.System)
	}
	if len(result.Added) != 1 || result.Added[0] != "returns-extractor" {
		t.Errorf("added = %v, want [returns-extractor]", result.Added)
	}
	if result.Version != "v1" {
		t.Errorf("version = %q, want v1", result.Version)
	}
	// Progress stays on stderr; stdout is the document alone.
	if !strings.Contains(stderr.String(), "updating "+hostDir) {
		t.Errorf("stderr missing progress line:\n%s", stderr.String())
	}

	// A real update leaves a complete declaration on disk.
	systemClass := readFile(t, filepath.Join(hostDir, "OrderFlowSystem.cs"))
	if !strings.Contains(systemClass, `builder.AddExtractor("returns-extractor")`) {
		t.Errorf("OrderFlowSystem.cs does not declare returns-extractor:\n%s", systemClass)
	}
}
