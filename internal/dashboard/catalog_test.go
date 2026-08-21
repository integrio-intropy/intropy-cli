package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// testTopology is the graph of the order-flow system these tests join
// against: one extractor with a publication and a subscription, and a
// contracts registry to resolve them against.
func testTopology() topology.Entry {
	return topology.Entry{
		Path: "order-flow",
		Topology: topology.Topology{
			APIVersion: topology.APIVersion,
			Kind:       topology.Kind,
			System:     "order-flow",
			Components: []topology.Component{{
				Name: "order-extractor",
				Kind: "extractor",
				Publishes: []topology.Publication{
					{PubSub: "pubsub", Topic: "orders-raw"},
				},
				Subscribes: []topology.TopicRef{
					{PubSub: "pubsub", Topic: "orders-in"},
				},
			}},
			Topics: []topology.Topic{
				{PubSub: "pubsub", Topic: "orders-raw", Contract: "Integrio.Contracts.RawOrder"},
				{PubSub: "pubsub", Topic: "orders-in", Contract: "Integrio.Contracts.Order"},
			},
			Contracts: []topology.Contract{
				{Name: "Integrio.Contracts.RawOrder", ShortName: "RawOrder"},
				{Name: "Integrio.Contracts.Order", ShortName: "Order"},
			},
		},
	}
}

// topoOnce returns a provider that serves the given entries, so the handler's
// cache is warm by the second request.
func topoOnce(entries []topology.Entry, errs []string) topologyProvider {
	return func(context.Context) ([]topology.Entry, []string) { return entries, errs }
}

// warmCatalog primes the topology cache (any /api/topology request does that)
// and then serves the catalog for the path, decoding the entry.
func warmCatalog(t *testing.T, h http.Handler, path string) CatalogEntry {
	t.Helper()
	if rec := get(t, h, "/api/topology"); rec.Code != http.StatusOK {
		t.Fatalf("warm-up status = %d", rec.Code)
	}
	rec := get(t, h, "/api/catalog/"+path)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog status = %d: %s", rec.Code, rec.Body.String())
	}
	var entry CatalogEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	return entry
}

// The header case: graph facts win, contracts resolve against the registry
// the same way the flow view resolves them (shortName when present).
func TestCatalogMatchedResolvesContracts(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "order-extractor"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "order-flow", "host"), "order-flow")

	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{testTopology()}, nil))
	entry := warmCatalog(t, h, "order-flow/order-extractor")

	if entry.GraphStatus != "matched" {
		t.Fatalf("graphStatus = %q, want matched", entry.GraphStatus)
	}
	if entry.Component != "order-extractor" || entry.Kind != "extractor" {
		t.Errorf("identity = %q/%q, want order-extractor/extractor", entry.Component, entry.Kind)
	}
	if entry.System != "order-flow" {
		t.Errorf("system = %q, want order-flow", entry.System)
	}
	if entry.Repository != "integrio-intropy/intropy-templates" {
		t.Errorf("repository = %q", entry.Repository)
	}
	if len(entry.Publishes) != 1 {
		t.Fatalf("publishes = %+v", entry.Publishes)
	}
	pub := entry.Publishes[0]
	if pub.Topic != "orders-raw" || pub.Pubsub != "pubsub" || pub.Contract != "RawOrder" {
		t.Errorf("publication = %+v, want orders-raw with shortName RawOrder", pub)
	}
	if len(entry.Subscribes) != 1 || entry.Subscribes[0].Contract != "Order" {
		t.Errorf("subscribes = %+v, want orders-in with shortName Order", entry.Subscribes)
	}
	if len(entry.Checks) != 0 {
		t.Errorf("checks = %+v, want none", entry.Checks)
	}
}

// A contract the registry does not carry renders under its raw name, as the
// flow view's detail panel renders it.
func TestCatalogContractWithoutRegistryEntryUsesRawName(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "order-extractor"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "order-flow", "host"), "order-flow")

	topo := testTopology()
	topo.Contracts = nil // host predates the registry
	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{topo}, nil))
	entry := warmCatalog(t, h, "order-flow/order-extractor")

	if entry.GraphStatus != "matched" {
		t.Fatalf("graphStatus = %q, want matched", entry.GraphStatus)
	}
	if got := entry.Publishes[0].Contract; got != "Integrio.Contracts.RawOrder" {
		t.Errorf("contract = %q, want the raw name", got)
	}
}

// An integration with no system at all has no graph to join against.
func TestCatalogNoTopologyWithoutSystem(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "standalone"), "extractor", "v0.2.0")

	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{testTopology()}, nil))
	entry := warmCatalog(t, h, "standalone")

	if entry.GraphStatus != "no-topology" {
		t.Fatalf("graphStatus = %q, want no-topology", entry.GraphStatus)
	}
	if entry.Component != "standalone" {
		t.Errorf("component = %q, want the scaffold name", entry.Component)
	}
	if len(entry.Checks) != 0 {
		t.Errorf("checks = %+v, want none — there is nothing to be absent from", entry.Checks)
	}
}

// A folder-derived "system" is a normal workspace state: a component whose
// folder looks like a system but that no graph declares must never render as
// drift.
func TestCatalogFolderDerivedSystemIsNotDrift(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	// No system host: the "order-flow" grouping is folder convention only.
	writeScaffold(t, filepath.Join(tmp, "order-flow", "order-extractor"), "extractor", "v0.2.0")

	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{testTopology()}, nil))
	entry := warmCatalog(t, h, "order-flow/order-extractor")

	// The topology's path matches the folder, and the component is in it:
	// the join succeeds on folder membership alone.
	if entry.GraphStatus != "matched" {
		t.Fatalf("graphStatus = %q, want matched", entry.GraphStatus)
	}
	if len(entry.Checks) != 0 {
		t.Errorf("checks = %+v, want none", entry.Checks)
	}
}

// The folder-derived case that matters: the component is NOT in the graph.
// Without a declared host that is still not drift.
func TestCatalogFolderDerivedAbsentFromGraphIsSilent(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "stock-sync"), "extractor", "v0.2.0")

	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{testTopology()}, nil))
	entry := warmCatalog(t, h, "order-flow/stock-sync")

	if entry.GraphStatus != "no-topology" {
		t.Fatalf("graphStatus = %q, want no-topology", entry.GraphStatus)
	}
	for _, c := range entry.Checks {
		if c.Severity == "warn" {
			t.Errorf("a folder-derived system must never warn: %+v", c)
		}
	}
}

// A component the graph of its declared system does not declare is the one
// case worth a warning: something was renamed without re-scaffolding.
func TestCatalogNotInGraphWarnsForDeclaredHost(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "stock-sync"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "order-flow", "host"), "order-flow")

	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{testTopology()}, nil))
	entry := warmCatalog(t, h, "order-flow/stock-sync")

	if entry.GraphStatus != "not-in-graph" {
		t.Fatalf("graphStatus = %q, want not-in-graph", entry.GraphStatus)
	}
	if len(entry.Checks) != 1 || entry.Checks[0].Severity != "warn" {
		t.Fatalf("checks = %+v, want one warn", entry.Checks)
	}
	if !containsAll(entry.Checks[0].Message, "stock-sync", "does not declare") {
		t.Errorf("check message = %q", entry.Checks[0].Message)
	}
	// Identity falls back to the scaffold record.
	if entry.Component != "stock-sync" {
		t.Errorf("component = %q, want the scaffold name", entry.Component)
	}
}

// A host whose graph verb failed passes its own error through as an info
// check; the catalog does not guess around it.
func TestCatalogTopologyErrorPassesHostMessageThrough(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "order-extractor"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "order-flow", "host"), "order-flow")

	errs := []string{"order-flow: graph verb failed: exit status 1"}
	h := testHandlerWithTopo(t, ".", topoOnce(nil, errs))
	entry := warmCatalog(t, h, "order-flow/order-extractor")

	if entry.GraphStatus != "topology-error" {
		t.Fatalf("graphStatus = %q, want topology-error", entry.GraphStatus)
	}
	if len(entry.Checks) != 1 || entry.Checks[0].Severity != "info" {
		t.Fatalf("checks = %+v, want one info", entry.Checks)
	}
	if entry.Checks[0].Message != errs[0] {
		t.Errorf("check message = %q, want the host's own %q", entry.Checks[0].Message, errs[0])
	}
}

// A catalog request against a cold topology cache answers promptly with
// scaffold identity rather than blocking on the hosts' graph verbs.
func TestCatalogPendingWhenCacheIsCold(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "order-extractor"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "order-flow", "host"), "order-flow")

	var calls atomic.Int64
	provider := func(context.Context) ([]topology.Entry, []string) {
		calls.Add(1)
		return nil, nil
	}
	h := testHandlerWithTopo(t, ".", provider)

	rec := get(t, h, "/api/catalog/order-flow/order-extractor")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var entry CatalogEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if entry.GraphStatus != "pending" || !entry.TopologyPending {
		t.Errorf("graphStatus/topologyPending = %q/%v, want pending/true", entry.GraphStatus, entry.TopologyPending)
	}
	if entry.Component != "order-extractor" {
		t.Errorf("component = %q, want the scaffold name even while pending", entry.Component)
	}

	// The pending answer must not stay pending: the request triggers a
	// background warm-up, so once it finishes the next fetch joins the graph.
	// The warm-up runs the provider asynchronously; wait for it rather than
	// racing it.
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("a pending answer should warm the topology cache in the background")
	}
}

// The full cold-start arc: the first catalog request answers pending and
// warms the cache in the background; a later request serves the joined entry.
// This is the dashboard's default view, so it must converge without anyone
// visiting the flow view first.
func TestCatalogPendingResolvesAfterWarmUp(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	writeScaffold(t, filepath.Join(tmp, "order-flow", "order-extractor"), "extractor", "v0.2.0")
	writeSystemHost(t, filepath.Join(tmp, "order-flow", "host"), "order-flow")

	h := testHandlerWithTopo(t, ".", topoOnce([]topology.Entry{testTopology()}, nil))

	rec := get(t, h, "/api/catalog/order-flow/order-extractor")
	var first CatalogEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.GraphStatus != "pending" {
		t.Fatalf("first graphStatus = %q, want pending", first.GraphStatus)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		rec := get(t, h, "/api/catalog/order-flow/order-extractor")
		var entry CatalogEntry
		if err := json.Unmarshal(rec.Body.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.GraphStatus == "pending" {
			continue
		}
		if entry.GraphStatus != "matched" {
			t.Fatalf("graphStatus = %q, want matched after warm-up", entry.GraphStatus)
		}
		if len(entry.Publishes) != 1 || entry.Publishes[0].Contract != "RawOrder" {
			t.Errorf("publishes = %+v, want the resolved contract", entry.Publishes)
		}
		return
	}
	t.Fatal("catalog still pending 5s after the warm-up started")
}

// Unknown paths 404, as on the detail endpoint: the two agree about which
// identifiers name an integration.
func TestCatalogNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	rec := get(t, testHandler(t, "."), "/api/catalog/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
