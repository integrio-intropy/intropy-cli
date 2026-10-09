package system

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func TestMergeWiringAddsScalarMessageOrphan(t *testing.T) {
	plan := &updatePlan{
		hostDir: "host",
		baseline: map[string]any{
			"components": []any{},
			"topics":     []any{},
			"ports":      []any{},
			"messages":   []any{},
		},
		orphans: []Component{{
			AppID: "order-extractor", Kind: template.BlockKindExtractor,
			Topic: &TopicKey{Pubsub: template.DefaultPubsub, Name: "order-created"}, topicContract: "OrderCreated",
			Ports:   []string{"order-source"},
			Message: &MessageWiring{Kind: MessagePublish, Name: "order-created", Contract: "OrderCreated"},
		}},
	}
	merged := map[string]any{}
	if err := mergeWiring(plan, merged); err != nil {
		t.Fatalf("mergeWiring: %v", err)
	}
	messages := merged["messages"].([]any)
	m := messages[0].(map[string]any)
	if m["name"] != "order-created" || m["contract"] != "OrderCreated" {
		t.Fatalf("message = %#v", m)
	}
}

// assembledLoader assembles a producer and a loader and returns the loader.
func assembledLoader(t *testing.T, l template.ScaffoldEntry) Component {
	t.Helper()
	model, err := Assemble([]template.ScaffoldEntry{
		channeled("order-extractor", "order-placed", "orders"),
		l,
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return model.Components[1]
}

// storedEntry is a component entry as the host record holds it after the
// JSON round-trip.
func storedEntry(t *testing.T, c Component) map[string]any {
	t.Helper()
	data, err := json.Marshal(ComponentEntry(c))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A stored entry that says what the scaffold says is not a refresh, so an
// update over unchanged components stays a no-op.
func TestRefreshedEntryOfAnUnchangedComponentIsEqual(t *testing.T) {
	c := assembledLoader(t, routingLoader("order-loader", "orders", "",
		template.Route{Message: "order-placed", When: "event.data.total > 0"}))
	stored := storedEntry(t, c)
	if got := refreshedEntry(stored, c); !reflect.DeepEqual(got, stored) {
		t.Errorf("refreshed = %#v\nstored    = %#v", got, stored)
	}
}

// A component's changed filter reaches its stored entry; keys recorded by
// hand survive the rewrite.
func TestRefreshedEntryTakesTheScaffoldsRoutes(t *testing.T) {
	before := assembledLoader(t, routingLoader("order-loader", "orders", "",
		template.Route{Message: "order-placed"}))
	stored := storedEntry(t, before)
	stored["note"] = "kept by hand"

	after := assembledLoader(t, routingLoader("order-loader", "orders", "",
		template.Route{Message: "order-placed", When: "event.data.reason != 'fraud-review'"}))
	got := refreshedEntry(stored, after)

	want := []any{map[string]any{
		template.KeyMessage: "order-placed",
		template.KeyWhen:    "event.data.reason != 'fraud-review'",
	}}
	if !reflect.DeepEqual(got[template.KeyRoutes], want) {
		t.Errorf("routes = %#v, want %#v", got[template.KeyRoutes], want)
	}
	if got["note"] != "kept by hand" {
		t.Errorf("hand-recorded key lost: %#v", got)
	}
}

// Refreshed entries are rewritten in place; untouched and vanished entries
// pass through; orphans are appended.
func TestMergedComponentEntriesRefreshesInPlace(t *testing.T) {
	after := assembledLoader(t, routingLoader("order-loader", "orders", "",
		template.Route{Message: "order-placed", When: "event.data.total > 0"}))
	vanished := map[string]any{template.KeyAppID: "gone-loader", "kind": "loader"}
	stale := map[string]any{template.KeyAppID: "order-loader", "kind": "loader", template.KeySubscribes: "order-placed"}
	orphan := Component{AppID: "new-extractor", Kind: template.BlockKindExtractor}

	got, err := mergedComponentEntries(&updatePlan{
		baseline:  map[string]any{"components": []any{vanished, stale}},
		refreshed: []Component{after},
		orphans:   []Component{orphan},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !reflect.DeepEqual(got[0], vanished) {
		t.Fatalf("components = %#v", got)
	}
	if routes, _ := got[1].(map[string]any)[template.KeyRoutes].([]any); len(routes) != 1 {
		t.Errorf("order-loader not refreshed: %#v", got[1])
	}
	if got[2].(map[string]any)[template.KeyAppID] != "new-extractor" {
		t.Errorf("orphan not appended: %#v", got[2])
	}
}
