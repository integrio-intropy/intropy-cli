package system

import (
	"path/filepath"
	"testing"
)

func testModel() *Model {
	return &Model{
		Name: "order-flow",
		Topics: []Topic{
			{TopicKey: TopicKey{Pubsub: "pubsub", Name: "orders"}, Contract: "Order"},
		},
		Connectors: []Connector{
			{Name: "erp-destination"},
			{Name: "erp-source"},
			{Name: "order-extractor-source"},
			{Name: "order-loader-destination"},
		},
		Components: []Component{
			{AppID: "order-extractor", Kind: "extractor", Topic: &TopicKey{Pubsub: "pubsub", Name: "orders"}, Connector: "order-extractor-source", Connectors: []string{"order-extractor-source"}},
			{AppID: "order-loader", Kind: "loader", Topic: &TopicKey{Pubsub: "pubsub", Name: "orders"}, Connector: "order-loader-destination", Connectors: []string{"order-loader-destination"}},
			{AppID: "audit-loader", Kind: "loader", Topic: &TopicKey{Pubsub: "pubsub", Name: "orders"}},
			{AppID: "erp-sync", Kind: "transactional-integration", Connectors: []string{"erp-source", "erp-destination"}},
		},
		Shared: &SharedLibrary{Path: "Contracts", Name: "Contracts"},
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
	wantTopic := map[string]any{"pubsub": "pubsub", "name": "orders", "contract": "Order"}
	for k, want := range wantTopic {
		if topic[k] != want {
			t.Errorf("topic[%s] = %v, want %v", k, topic[k], want)
		}
	}

	connectors, ok := payload["connectors"].([]any)
	if !ok || len(connectors) != 4 {
		t.Fatalf("connectors = %#v", payload["connectors"])
	}
	first := connectors[0].(map[string]any)
	if first["name"] != "erp-destination" || len(first) != 1 {
		t.Errorf("connector[0] = %#v, want only the name", first)
	}

	components, ok := payload["components"].([]any)
	if !ok || len(components) != 4 {
		t.Fatalf("components = %#v", payload["components"])
	}
	extractor := components[0].(map[string]any)
	if extractor["appId"] != "order-extractor" || extractor["kind"] != "extractor" {
		t.Errorf("extractor = %#v", extractor)
	}
	wantTopicKey := map[string]any{"pubsub": "pubsub", "name": "orders"}
	topicKey, ok := extractor["topic"].(map[string]any)
	if !ok || topicKey["pubsub"] != wantTopicKey["pubsub"] || topicKey["name"] != wantTopicKey["name"] {
		t.Errorf("extractor topic = %#v, want %#v", extractor["topic"], wantTopicKey)
	}
	if extractor["connector"] != "order-extractor-source" {
		t.Errorf("extractor connector = %#v", extractor["connector"])
	}
	loader := components[1].(map[string]any)
	if loader["kind"] != "loader" || loader["connector"] != "order-loader-destination" {
		t.Errorf("loader = %#v", loader)
	}
	// A component without a connector carries no connector key at all; the
	// template renders no From/To for it.
	audit := components[2].(map[string]any)
	if _, ok := audit["connector"]; ok {
		t.Errorf("connector-less component should carry no connector key: %#v", audit)
	}
	if _, ok := audit["topic"]; !ok {
		t.Errorf("connector-less component still carries its topic: %#v", audit)
	}
	// A transactional component emits from/to and no topic.
	tx := components[3].(map[string]any)
	if tx["kind"] != "transactional-integration" {
		t.Errorf("transactional kind = %v", tx["kind"])
	}
	if tx["from"] != "erp-source" || tx["to"] != "erp-destination" {
		t.Errorf("transactional wiring = %#v", tx)
	}
	for _, absent := range []string{"topic", "connector"} {
		if _, ok := tx[absent]; ok {
			t.Errorf("transactional component should not carry %s: %#v", absent, tx)
		}
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

func TestBuildPayloadIsFactsOnly(t *testing.T) {
	payload, err := buildPayload(testModel(), filepath.Join(t.TempDir(), "order-flow"), "order-flow")
	if err != nil {
		t.Fatal(err)
	}

	// No CLI-derived rendering identifiers anywhere in the payload: field
	// names and the component-to-field joins are the template's to derive.
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch node := v.(type) {
		case map[string]any:
			for k, vv := range node {
				for _, banned := range []string{"field", "topicField", "connectorField", "fromField", "toField"} {
					if k == banned {
						t.Errorf("payload key %s at %s: rendering knowledge belongs in the template", k, path)
					}
				}
				walk(vv, path+"."+k)
			}
		case []any:
			for _, vv := range node {
				walk(vv, path)
			}
		}
	}
	walk(payload, "payload")
}

func TestBuildPayloadTransactionalOnly(t *testing.T) {
	m := &Model{
		Name: "trans",
		Connectors: []Connector{
			{Name: "erp-destination"},
			{Name: "erp-source"},
		},
		Components: []Component{
			{AppID: "erp-sync", Kind: "transactional-integration", Connectors: []string{"erp-source", "erp-destination"}},
		},
	}

	payload, err := buildPayload(m, "trans", "trans")
	if err != nil {
		t.Fatal(err)
	}

	topics, ok := payload["topics"].([]any)
	if !ok || len(topics) != 0 {
		t.Errorf("topics = %#v, want an (empty) list so the template can range over it", payload["topics"])
	}
	if _, ok := payload["sharedContracts"]; ok {
		t.Errorf("sharedContracts should be absent when the workspace has none: %#v", payload["sharedContracts"])
	}
	components := payload["components"].([]any)
	tx := components[0].(map[string]any)
	if tx["from"] != "erp-source" || tx["to"] != "erp-destination" {
		t.Errorf("transactional wiring = %#v", tx)
	}
}

func TestBuildPayloadWithoutConnectors(t *testing.T) {
	m := testModel()
	m.Connectors = nil
	m.Components = m.Components[:1]
	m.Components[0].Connector = ""
	m.Components[0].Connectors = nil

	payload, err := buildPayload(m, "order-flow", "order-flow")
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
