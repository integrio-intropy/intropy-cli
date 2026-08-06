package system

import (
	"path/filepath"
	"testing"
)

func testModel() *Model {
	return &Model{
		Name: "order-flow",
		Topics: []Topic{
			{TopicKey: TopicKey{Pubsub: "pubsub", Name: "orders"}, Contract: "Order", Field: "Orders"},
		},
		Connectors: []Connector{
			{Name: "order-extractor-source", Field: "OrderExtractorSource"},
			{Name: "order-loader-destination", Field: "OrderLoaderDestination"},
		},
		Components: []Component{
			{AppID: "order-extractor", Kind: "extractor", Topic: TopicKey{Pubsub: "pubsub", Name: "orders"}, Connector: "order-extractor-source"},
			{AppID: "order-loader", Kind: "loader", Topic: TopicKey{Pubsub: "pubsub", Name: "orders"}, Connector: "order-loader-destination"},
			{AppID: "audit-loader", Kind: "loader", Topic: TopicKey{Pubsub: "pubsub", Name: "orders"}},
		},
		Shared: SharedLibrary{Path: "Contracts", Name: "Contracts"},
	}
}

func TestBuildPayloadShape(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "order-flow")
	payload, err := buildPayload(testModel(), outDir, "order-flow")
	if err != nil {
		t.Fatal(err)
	}

	if payload["name"] != "order-flow" {
		t.Errorf("name = %v", payload["name"])
	}

	topics, ok := payload["topics"].([]any)
	if !ok || len(topics) != 1 {
		t.Fatalf("topics = %#v", payload["topics"])
	}
	topic := topics[0].(map[string]any)
	wantTopic := map[string]any{"pubsub": "pubsub", "name": "orders", "contract": "Order", "field": "Orders"}
	for k, want := range wantTopic {
		if topic[k] != want {
			t.Errorf("topic[%s] = %v, want %v", k, topic[k], want)
		}
	}

	connectors, ok := payload["connectors"].([]any)
	if !ok || len(connectors) != 2 {
		t.Fatalf("connectors = %#v", payload["connectors"])
	}
	first := connectors[0].(map[string]any)
	if first["name"] != "order-extractor-source" || first["field"] != "OrderExtractorSource" {
		t.Errorf("connector[0] = %#v", first)
	}

	components, ok := payload["components"].([]any)
	if !ok || len(components) != 3 {
		t.Fatalf("components = %#v", payload["components"])
	}
	extractor := components[0].(map[string]any)
	if extractor["appId"] != "order-extractor" || extractor["kind"] != "extractor" {
		t.Errorf("extractor = %#v", extractor)
	}
	if extractor["topicField"] != "Orders" || extractor["connectorField"] != "OrderExtractorSource" {
		t.Errorf("extractor joins = %#v", extractor)
	}
	loader := components[1].(map[string]any)
	if loader["kind"] != "loader" || loader["connectorField"] != "OrderLoaderDestination" {
		t.Errorf("loader = %#v", loader)
	}
	// A component without a connector carries an empty join, not a missing key.
	audit := components[2].(map[string]any)
	if audit["connectorField"] != "" {
		t.Errorf("connector-less component connectorField = %v, want empty string", audit["connectorField"])
	}
	if audit["topicField"] != "Orders" {
		t.Errorf("connector-less component topicField = %v", audit["topicField"])
	}

	shared, ok := payload["sharedContracts"].(map[string]any)
	if !ok {
		t.Fatalf("sharedContracts = %#v", payload["sharedContracts"])
	}
	if shared["name"] != "Contracts" {
		t.Errorf("sharedContracts.name = %v", shared["name"])
	}
	absShared, err := filepath.Abs("Contracts")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(outDir, absShared)
	if err != nil {
		t.Fatal(err)
	}
	wantInclude := filepath.ToSlash(filepath.Join(rel, "Contracts.csproj"))
	if shared["include"] != wantInclude {
		t.Errorf("sharedContracts.include = %v, want %v", shared["include"], wantInclude)
	}
}

func TestBuildPayloadWithoutConnectors(t *testing.T) {
	m := testModel()
	m.Connectors = nil
	m.Components = m.Components[:1]
	m.Components[0].Connector = ""

	payload, err := buildPayload(m, filepath.Join(t.TempDir(), "order-flow"), "order-flow")
	if err != nil {
		t.Fatal(err)
	}
	connectors, ok := payload["connectors"].([]any)
	if !ok {
		t.Fatalf("connectors = %#v, want an (empty) list so the template can range over it", payload["connectors"])
	}
	if len(connectors) != 0 {
		t.Errorf("connectors = %#v, want empty", connectors)
	}
}
