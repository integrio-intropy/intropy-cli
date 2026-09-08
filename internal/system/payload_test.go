package system

import (
	"path/filepath"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func testModel() *Model {
	return &Model{
		Name: "order-flow",
		Topics: []Topic{
			{TopicKey: TopicKey{Pubsub: "pubsub", Name: "orders"}, Contract: "Order"},
		},
		Ports: []Port{
			{Name: "erp-destination"},
			{Name: "erp-source"},
			{Name: "order-extractor-source"},
			{Name: "order-loader-destination"},
		},
		Components: []Component{
			{AppID: "order-extractor", Kind: "extractor", Topic: &TopicKey{Pubsub: "pubsub", Name: "orders"}, Port: "order-extractor-source", Ports: []string{"order-extractor-source"}},
			{AppID: "order-loader", Kind: "loader", Topic: &TopicKey{Pubsub: "pubsub", Name: "orders"}, Port: "order-loader-destination", Ports: []string{"order-loader-destination"}},
			{AppID: "audit-loader", Kind: "loader", Topic: &TopicKey{Pubsub: "pubsub", Name: "orders"}},
			{AppID: "erp-sync", Kind: "transactional-integration", Ports: []string{"erp-source", "erp-destination"}},
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

	ports, ok := payload["ports"].([]any)
	if !ok || len(ports) != 4 {
		t.Fatalf("ports = %#v", payload["ports"])
	}
	first := ports[0].(map[string]any)
	if first["name"] != "erp-destination" || len(first) != 1 {
		t.Errorf("port[0] = %#v, want only the name", first)
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
	if extractor["port"] != "order-extractor-source" {
		t.Errorf("extractor port = %#v", extractor["port"])
	}
	loader := components[1].(map[string]any)
	if loader["kind"] != "loader" || loader["port"] != "order-loader-destination" {
		t.Errorf("loader = %#v", loader)
	}
	// A component without a port carries no port key at all; the template
	// renders no From/To for it.
	audit := components[2].(map[string]any)
	if _, ok := audit["port"]; ok {
		t.Errorf("port-less component should carry no port key: %#v", audit)
	}
	if _, ok := audit["topic"]; !ok {
		t.Errorf("port-less component still carries its topic: %#v", audit)
	}
	// A transactional component emits fromPort/toPort (the keys the
	// template library's system-host reads) and no topic.
	tx := components[3].(map[string]any)
	if tx["kind"] != "transactional-integration" {
		t.Errorf("transactional kind = %v", tx["kind"])
	}
	if tx["fromPort"] != "erp-source" || tx["toPort"] != "erp-destination" {
		t.Errorf("transactional wiring = %#v", tx)
	}
	for _, absent := range []string{"topic", "port", "from", "to"} {
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
				for _, banned := range []string{"field", "topicField", "portField", "fromField", "toField"} {
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
		Ports: []Port{
			{Name: "erp-destination"},
			{Name: "erp-source"},
		},
		Components: []Component{
			{AppID: "erp-sync", Kind: "transactional-integration", Ports: []string{"erp-source", "erp-destination"}},
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
	if tx["fromPort"] != "erp-source" || tx["toPort"] != "erp-destination" {
		t.Errorf("transactional wiring = %#v", tx)
	}
}

func TestBuildPayloadWithoutPorts(t *testing.T) {
	m := testModel()
	m.Ports = nil
	m.Components = m.Components[:1]
	m.Components[0].Port = ""
	m.Components[0].Ports = nil

	payload, err := buildPayload(m, "order-flow", "order-flow")
	if err != nil {
		t.Fatal(err)
	}
	ports, ok := payload["ports"].([]any)
	if !ok {
		t.Fatalf("ports = %#v, want an (empty) list so the template can range over it", payload["ports"])
	}
	if len(ports) != 0 {
		t.Errorf("ports = %#v, want empty", ports)
	}
}

func TestBuildPayloadMessagegroup(t *testing.T) {
	m := &Model{
		Name: "order-flow",
		Components: []Component{
			{AppID: "product-sink", Kind: template.BlockKindExtractor,
				Topic:   &TopicKey{Pubsub: template.DefaultPubsub, Name: "product-exported"},
				Message: &MessageWiring{Kind: MessagePublish, Name: "product-exported", Contract: "ProductExported"}},
			{AppID: "product-loader", Kind: template.BlockKindLoader,
				Topic:   &TopicKey{Pubsub: template.DefaultPubsub, Name: "product-exported"},
				Message: &MessageWiring{Kind: MessageSubscribe, Name: "product-exported"}},
			{AppID: "external-loader", Kind: template.BlockKindLoader,
				Topic:   &TopicKey{Pubsub: "ext-pubsub", Name: "ext-topic"},
				Message: &MessageWiring{Kind: MessageSubscribe, Name: "io.registry/export", Dataschema: "/schemagroups/g/schemas/s", External: true}},
		},
		Topics: []Topic{
			{TopicKey: TopicKey{Pubsub: template.DefaultPubsub, Name: "product-exported"}, Contract: "ProductExported"},
			{TopicKey: TopicKey{Pubsub: "ext-pubsub", Name: "ext-topic"}},
		},
		Messages: []Message{{Name: "product-exported", Type: "product-exported", Contract: "ProductExported", Publisher: "product-sink"}},
		Ports:    []Port{},
	}
	payload, err := buildPayload(m, ".", "order-flow")
	if err != nil {
		t.Fatal(err)
	}

	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %#v, want one messagegroup entry", payload["messages"])
	}
	msg, _ := msgs[0].(map[string]any)
	if msg["name"] != "product-exported" || msg["type"] != "product-exported" || msg["contract"] != "ProductExported" {
		t.Errorf("message entry = %#v, want name/type/contract", msg)
	}

	// Component entries carry the message name so the template joins the
	// two views; dataschema travels only on the external snapshot.
	comps := payload["components"].([]any)
	if m2, _ := comps[0].(map[string]any); m2["message"] != "product-exported" {
		t.Errorf("publisher entry message = %#v", m2["message"])
	}
	if m3, _ := comps[2].(map[string]any); m3["dataschema"] != "/schemagroups/g/schemas/s" {
		t.Errorf("external entry dataschema = %#v", m3["dataschema"])
	}
	if m2, _ := comps[1].(map[string]any); m2["dataschema"] != nil {
		t.Errorf("internal subscriber carries no dataschema, got %#v", m2["dataschema"])
	}
}
