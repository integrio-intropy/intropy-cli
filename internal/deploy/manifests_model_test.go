package deploy

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
	"github.com/integrio-intropy/intropy-cli/internal/topology"
)

// A record covering what the model has to derive: two wired components, a
// topic with a subscriber that is not a component of this system, two ports,
// and an unwired component.
const initTopologyRecord = `{
  "apiVersion": "topology.intropy.io/v1",
  "kind": "SystemTopology",
  "system": "distribution",
  "components": [
    {
      "name": "erp-loader",
      "kind": "loader",
      "subscribes": [{"pubsub": "price-pubsub", "topic": "price-b2b"}],
      "ports": [{"port": "erp", "direction": "out"}]
    },
    {
      "name": "extractor",
      "kind": "extractor",
      "publishes": [
        {"pubsub": "price-pubsub", "topic": "price-b2b"},
        {"pubsub": "price-pubsub", "topic": "price-b2c"}
      ],
      "ports": [{"port": "price-master", "direction": "in"}]
    },
    {"name": "reconciler", "kind": "reconciler"}
  ],
  "topics": [
    {"pubsub": "price-pubsub", "topic": "price-b2c", "contract": "Price.Contracts.B2CPrice",
     "publishers": ["extractor"], "subscribers": ["wms-loader"]},
    {"pubsub": "price-pubsub", "topic": "price-b2b", "contract": "Price.Contracts.B2BPrice",
     "publishers": ["extractor"], "subscribers": ["erp-loader"]}
  ],
  "ports": [
    {"name": "price-master", "externalSystem": "price-master",
     "directions": ["in"], "usedBy": ["extractor"]},
    {"name": "erp", "externalSystem": "erp",
     "directions": ["out"], "usedBy": ["erp-loader"]}
  ]
}`

func decodeManifestTopology(t *testing.T) *topology.Topology {
	t.Helper()
	topo, err := topology.Decode(strings.NewReader(initTopologyRecord))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return topo
}

func findComponent(t *testing.T, m ManifestModel, name string) ManifestComponent {
	t.Helper()
	for _, c := range m.Components {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("component %q not in %+v", name, m.Components)
	return ManifestComponent{}
}

// The workload is the single most valuable thing the topology contributes: it is
// what the customer repos already do by hand.
func TestManifestModelWorkloadFromBlockKind(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)

	if got := findComponent(t, m, "extractor").Workload; got != WorkloadCronJob {
		t.Errorf("extractor workload = %q, want %q", got, WorkloadCronJob)
	}
	for _, name := range []string{"erp-loader", "reconciler"} {
		if got := findComponent(t, m, name).Workload; got != WorkloadDeployment {
			t.Errorf("%s workload = %q, want %q", name, got, WorkloadDeployment)
		}
	}
}

// The block kind is camelCase in some records and kebab-case in others, so the
// match must not be spelling-sensitive.
func TestManifestModelWorkloadIgnoresKindSpelling(t *testing.T) {
	for _, kind := range []string{"extractor", "Extractor", "EXTRACTOR"} {
		topo := &topology.Topology{
			APIVersion: topology.APIVersion,
			System:     "s",
			Components: []topology.Component{{Name: "x", Kind: kind}},
		}
		if got := newManifestModel(topo, nil).Components[0].Workload; got != WorkloadCronJob {
			t.Errorf("kind %q gave workload %q", kind, got)
		}
	}
}

func TestManifestModelPubSubsAreDistinctWithSortedScopes(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)

	if len(m.PubSubs) != 1 {
		t.Fatalf("PubSubs = %+v, want one", m.PubSubs)
	}
	ps := m.PubSubs[0]
	if ps.Name != "price-pubsub" {
		t.Errorf("PubSub name = %q", ps.Name)
	}
	if got := strings.Join(ps.Topics, ","); got != "price-b2b,price-b2c" {
		t.Errorf("Topics = %q, want them sorted", got)
	}
	// scopes: is the union of publishers and subscribers, including wms-loader,
	// which this system does not deploy — dropping it would silently deny an app
	// access to the broker.
	if got := strings.Join(ps.AppIDs, ","); got != "erp-loader,extractor,wms-loader" {
		t.Errorf("AppIDs = %q", got)
	}
}

func TestManifestModelPorts(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)

	if len(m.Ports) != 2 {
		t.Fatalf("Ports = %+v", m.Ports)
	}
	// Sorted by name, not emission order: erp is declared second in the record.
	if m.Ports[0].Name != "erp" || m.Ports[1].Name != "price-master" {
		t.Errorf("ports not sorted: %q, %q", m.Ports[0].Name, m.Ports[1].Name)
	}
	erp := m.Ports[0]
	if erp.ExternalSystem != "erp" {
		t.Errorf("erp externalSystem = %q", erp.ExternalSystem)
	}
	if strings.Join(erp.Directions, ",") != "out" {
		t.Errorf("erp directions = %v", erp.Directions)
	}
	if strings.Join(erp.AppIDs, ",") != "erp-loader" {
		t.Errorf("erp appIds = %v", erp.AppIDs)
	}
}

// Without a scaffold record there is no appId to read, and the component's own
// name is the only honest answer.
func TestManifestModelAppIDFallsBackToName(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)
	c := findComponent(t, m, "erp-loader")
	if c.AppID != "erp-loader" {
		t.Errorf("AppID = %q, want the component name", c.AppID)
	}
	if c.Dir != "" {
		t.Errorf("Dir = %q, want empty without a scaffold", c.Dir)
	}
}

func TestManifestModelAppIDFromScaffold(t *testing.T) {
	root := t.TempDir()
	scaffolds := []template.ScaffoldEntry{{
		Path:     filepath.Join(root, "erp-loader"),
		Scaffold: template.Scaffold{Values: map[string]any{"appId": "int201"}},
	}}

	m := newManifestModel(decodeManifestTopology(t), scaffolds)
	c := findComponent(t, m, "erp-loader")
	if c.AppID != "int201" {
		t.Errorf("AppID = %q, want int201 from the scaffold record", c.AppID)
	}
	if c.Dir != "erp-loader" {
		t.Errorf("Dir = %q", c.Dir)
	}
}

// The directory and the appId are both used as the join key in different parts
// of the toolchain, so a record found only by appId must still match.
func TestManifestModelScaffoldMatchedByAppID(t *testing.T) {
	root := t.TempDir()
	scaffolds := []template.ScaffoldEntry{{
		Path:     filepath.Join(root, "SomeOtherFolderName"),
		Scaffold: template.Scaffold{Values: map[string]any{"appId": "erp-loader"}},
	}}

	m := newManifestModel(decodeManifestTopology(t), scaffolds)
	if got := findComponent(t, m, "erp-loader").Dir; got != "SomeOtherFolderName" {
		t.Errorf("Dir = %q, want the record matched by appId", got)
	}
}

// A component wired to nothing is valid topology and must still be deployable.
func TestManifestModelKeepsUnwiredComponent(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)
	c := findComponent(t, m, "reconciler")
	if len(c.Topics) != 0 || len(c.Ports) != 0 {
		t.Errorf("reconciler = %+v, want no wiring", c)
	}
}

func TestManifestModelComponentWiringIsSorted(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)
	c := findComponent(t, m, "extractor")
	if got := strings.Join(c.Topics, ","); got != "price-b2b,price-b2c" {
		t.Errorf("extractor topics = %q", got)
	}
	if got := strings.Join(c.Ports, ","); got != "price-master" {
		t.Errorf("extractor ports = %q", got)
	}
}

// Idempotent re-renders depend on byte-identical values, and neither Go map
// order nor host emission order is guaranteed.
func TestManifestModelIsDeterministic(t *testing.T) {
	first, err := json.Marshal(newManifestModel(decodeManifestTopology(t), nil))
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		next, err := json.Marshal(newManifestModel(decodeManifestTopology(t), nil))
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("model is not stable across runs:\n%s\n%s", first, next)
		}
	}
}

// The reserved-key injection goes through a JSON round-trip so index and sprig
// see uniform map[string]any rather than Go structs.
func TestManifestModelRoundTripsToMap(t *testing.T) {
	m := newManifestModel(decodeManifestTopology(t), nil)
	got, err := m.asMap("staging", nil)
	if err != nil {
		t.Fatalf("asMap: %v", err)
	}
	if got["system"] != "distribution" {
		t.Errorf("system = %v", got["system"])
	}
	comps, ok := got["components"].([]any)
	if !ok || len(comps) != 3 {
		t.Fatalf("components = %#v", got["components"])
	}
	first := comps[0].(map[string]any)
	if first["workload"] != WorkloadDeployment {
		t.Errorf("components[0].workload = %v", first["workload"])
	}
}
