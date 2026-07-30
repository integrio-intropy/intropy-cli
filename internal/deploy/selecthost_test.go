package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// host builds a scaffold record shaped like a real one. The directory layout
// mirrors what `intropy sys create` leaves behind, and what a customer repository
// actually contains:
//
//	domains/orders/order-flow/system-host   role=system-host  values.name=order-flow
//
// Note the host directory is a generic "system-host": the recorded name and the
// parent directory are the keys worth matching on.
func host(dir, recordedName string) template.ScaffoldEntry {
	e := template.ScaffoldEntry{
		Path:     dir,
		Scaffold: template.Scaffold{Role: template.RoleSystemHost},
	}
	if recordedName != "" {
		e.Values = map[string]any{"name": recordedName}
	}
	return e
}

func TestSelectHostWithOneHostNeedsNoSystem(t *testing.T) {
	h := host(filepath.Join("domains", "orders", "order-flow", "system-host"), "order-flow")
	got, err := selectHost([]template.ScaffoldEntry{h}, "", ".")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != h.Path {
		t.Errorf("got %q", got.Path)
	}
}

// Picking one arbitrarily would scaffold the wrong system into a customer's repo.
func TestSelectHostRefusesToGuessBetweenSeveral(t *testing.T) {
	hosts := []template.ScaffoldEntry{
		host(filepath.Join("domains", "orders", "order-flow", "system-host"), "order-flow"),
		host(filepath.Join("domains", "price", "distribution", "system-host"), "distribution"),
	}
	_, err := selectHost(hosts, "", "integrations")
	if err == nil {
		t.Fatal("expected an error with several hosts and no --system")
	}
	for _, want := range []string{"--system", "order-flow", "distribution", "order-flow/system-host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestSelectHostByRecordedName(t *testing.T) {
	hosts := []template.ScaffoldEntry{
		host(filepath.Join("domains", "orders", "order-flow", "system-host"), "order-flow"),
		host(filepath.Join("domains", "price", "distribution", "system-host"), "distribution"),
	}
	got, err := selectHost(hosts, "order-flow", "integrations")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Path, "order-flow") {
		t.Errorf("selected %q", got.Path)
	}
}

// The record may be missing or nameless on an older scaffold, so the system
// directory has to work as a key too.
func TestSelectHostBySystemDirectory(t *testing.T) {
	hosts := []template.ScaffoldEntry{
		host(filepath.Join("domains", "orders", "order-flow", "system-host"), ""),
		host(filepath.Join("domains", "price", "distribution", "system-host"), ""),
	}
	got, err := selectHost(hosts, "distribution", "integrations")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Path, "distribution") {
		t.Errorf("selected %q", got.Path)
	}
}

// sys create kebab-cases whatever it is given, so these all name one system and
// must select one host.
func TestSelectHostFoldsNameSpellings(t *testing.T) {
	hosts := []template.ScaffoldEntry{
		host(filepath.Join("domains", "orders", "order-flow", "system-host"), "order-flow"),
		host(filepath.Join("domains", "price", "distribution", "system-host"), "distribution"),
	}
	for _, spelling := range []string{"order-flow", "OrderFlow", "orderFlow", "ORDER-FLOW"} {
		got, err := selectHost(hosts, spelling, "integrations")
		if err != nil {
			t.Errorf("%q: %v", spelling, err)
			continue
		}
		if !strings.Contains(got.Path, "order-flow") {
			t.Errorf("%q selected %q", spelling, got.Path)
		}
	}
}

func TestSelectHostUnknownSystemListsWhatExists(t *testing.T) {
	hosts := []template.ScaffoldEntry{
		host(filepath.Join("domains", "orders", "order-flow", "system-host"), "order-flow"),
		host(filepath.Join("domains", "price", "distribution", "system-host"), "distribution"),
	}
	_, err := selectHost(hosts, "no-such-system", "integrations")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"no-such-system", "order-flow", "distribution"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

// With one host, --system that matches nothing is the older meaning: rename the
// tree segment. Unambiguous, because there is only one system to talk about.
func TestSelectHostWithOneHostTreatsSystemAsARename(t *testing.T) {
	h := host(filepath.Join("workspace", "system-host"), "distribution")
	got, err := selectHost([]template.ScaffoldEntry{h}, "product-distribution", ".")
	if err != nil {
		t.Fatalf("a single host should still be selected: %v", err)
	}
	if got.Path != h.Path {
		t.Errorf("got %q", got.Path)
	}
}

func TestSelectHostWithNoHosts(t *testing.T) {
	_, err := selectHost(nil, "", "somewhere")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "system workspace") {
		t.Errorf("error should say to run from a system workspace: %v", err)
	}
}

// Two hosts that both answer to the name say nothing about which was meant.
func TestSelectHostAmbiguousMatch(t *testing.T) {
	hosts := []template.ScaffoldEntry{
		host(filepath.Join("a", "order-flow", "system-host"), "order-flow"),
		host(filepath.Join("b", "order-flow", "system-host"), "order-flow"),
	}
	_, err := selectHost(hosts, "order-flow", "integrations")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	if !strings.Contains(err.Error(), "matches 2 system hosts") {
		t.Errorf("error = %v", err)
	}
}

// stubRunGraph replaces the graph verb and records which host it was asked
// about, so a test can prove --system picked the right one without dotnet.
func stubRunGraph(t *testing.T, record string) *string {
	t.Helper()
	original := runGraph
	var called string
	runGraph = func(_ context.Context, hostDir string) (*topology.Topology, error) {
		called = hostDir
		return topology.Decode(strings.NewReader(record))
	}
	t.Cleanup(func() { runGraph = original })
	return &called
}

// writeHostWorkspace lays out a multi-system workspace the way sys create does.
func writeHostWorkspace(t *testing.T, root string, systems ...string) {
	t.Helper()
	for _, s := range systems {
		dir := filepath.Join(root, "domains", "x", s, "system-host")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		err := template.WriteScaffold(dir, template.Scaffold{
			SchemaVersion: template.ScaffoldSchemaVersion,
			Template:      "system-host",
			Owner:         "o",
			Repo:          "r",
			Version:       "v1",
			Role:          template.RoleSystemHost,
			Values:        map[string]any{"name": s},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestInitSelectsTheNamedSystemInAMultiSystemWorkspace(t *testing.T) {
	f := newInitFixture(t)
	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "order-flow", "distribution")
	called := stubRunGraph(t, initTopologyRecord)

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = "" // force host discovery
	opts.SourceDir = workspace
	opts.System = "distribution"

	if err := Init(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	want := filepath.Join(workspace, "domains", "x", "distribution", "system-host")
	if *called != want {
		t.Errorf("graph verb ran on %q, want %q", *called, want)
	}
	// Only the selected system is built: the other host is never touched.
	if strings.Contains(*called, "order-flow") {
		t.Errorf("the wrong host was built: %q", *called)
	}
}

func TestInitWithoutSystemInAMultiSystemWorkspaceIsAnError(t *testing.T) {
	f := newInitFixture(t)
	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "order-flow", "distribution")
	called := stubRunGraph(t, initTopologyRecord)

	opts := f.options(&bytes.Buffer{}, &bytes.Buffer{})
	opts.TopologyFile = ""
	opts.SourceDir = workspace

	_, _, err := runInit(t, opts)
	if err == nil {
		t.Fatal("expected an error: two systems and nothing saying which")
	}
	if !strings.Contains(err.Error(), "--system") {
		t.Errorf("error should point at --system: %v", err)
	}
	// Nothing was built — discovery must not pay for a graph verb to fail.
	if *called != "" {
		t.Errorf("the graph verb ran anyway, on %q", *called)
	}
}

// Selecting by name and renaming the tree segment are the same flag, so when they
// disagree the caller's name wins — and is told.
func TestInitWarnsWhenSystemRenamesTheTreeSegment(t *testing.T) {
	f := newInitFixture(t)
	workspace := t.TempDir()
	writeHostWorkspace(t, workspace, "distribution")
	stubRunGraph(t, initTopologyRecord) // declares system "distribution"

	var stdout, stderr bytes.Buffer
	opts := f.options(&stdout, &stderr)
	opts.TopologyFile = ""
	opts.SourceDir = workspace
	opts.System = "product-distribution"
	opts.OutputFormat = OutputJSON

	if err := Init(context.Background(), opts); err != nil {
		t.Fatalf("Init: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "using \"product-distribution\" as the tree segment") {
		t.Errorf("stderr does not warn about the rename:\n%s", stderr.String())
	}

	var res InitResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.System != "product-distribution" {
		t.Errorf("System = %q, want the caller's name", res.System)
	}
}
