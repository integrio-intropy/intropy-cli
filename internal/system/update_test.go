package system

import (
	"encoding/json"
	"io"
	"path/filepath"
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
			Topic: &TopicKey{Pubsub: "order-flow-pubsub", Name: "order-created"}, topicContract: "OrderCreated",
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

// A host created before systems owned their pub/sub declares the legacy name;
// an update reads it back so folded-in components stay on the host's one.
func TestStoredPubsub(t *testing.T) {
	cases := map[string]struct {
		values map[string]any
		want   string
	}{
		"legacy topics": {map[string]any{"topics": []any{
			map[string]any{template.KeyPubsub: "pubsub", template.KeyName: "orders"}}}, "pubsub"},
		"messages only": {map[string]any{"messages": []any{
			map[string]any{template.KeyName: "orders", template.KeyPubsub: "order-flow-pubsub", template.KeyTopic: "orders"}}}, "order-flow-pubsub"},
		"message on its own name declares none": {map[string]any{"messages": []any{
			map[string]any{template.KeyName: "orders"}}}, ""},
		"transactional only": {map[string]any{"topics": []any{}, "messages": []any{}}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := storedPubsub(tc.values); got != tc.want {
				t.Errorf("storedPubsub = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMergeWiringAddsGatewayEndpoint(t *testing.T) {
	plan := &updatePlan{
		hostDir: "host",
		baseline: map[string]any{
			"components": []any{},
			"topics":     []any{},
			"ports":      []any{},
			"messages":   []any{},
		},
		orphans: []Component{{
			AppID: "order-gateway", Kind: template.BlockKindGateway, Endpoint: "order-gateway-endpoint", EndpointPath: "/order-placed",
			Topic: &TopicKey{Pubsub: "orders-pubsub", Name: "order-placed"}, topicContract: "OrderPlaced",
			Message: &MessageWiring{Kind: MessagePublish, Name: "order-placed", Contract: "OrderPlaced"},
		}},
	}
	merged := map[string]any{}
	if err := mergeWiring(plan, merged); err != nil {
		t.Fatalf("mergeWiring: %v", err)
	}
	endpoints := merged["endpoints"].([]any)
	if e := endpoints[0].(map[string]any); len(endpoints) != 1 || e["name"] != "order-gateway-endpoint" || e["path"] != "/order-placed" {
		t.Fatalf("endpoints = %#v", endpoints)
	}
	if ports := merged["ports"].([]any); len(ports) != 0 {
		t.Fatalf("ports = %#v, want empty", ports)
	}
	if m := merged["messages"].([]any)[0].(map[string]any); m["publisher"] != "order-gateway" {
		t.Fatalf("message = %#v", m)
	}
}

// Stored endpoints pass through verbatim and an orphan's is appended, as for
// ports. (A record without the list starts one: the test above.)
func TestMergeWiringKeepsStoredEndpoints(t *testing.T) {
	plan := &updatePlan{
		hostDir: "host",
		baseline: map[string]any{
			"endpoints": []any{map[string]any{"name": "a-endpoint"}},
		},
		orphans: []Component{{AppID: "b", Kind: template.BlockKindGateway, Endpoint: "b-endpoint"}},
	}
	merged := map[string]any{}
	if err := mergeWiring(plan, merged); err != nil {
		t.Fatalf("mergeWiring: %v", err)
	}
	if endpoints := merged["endpoints"].([]any); len(endpoints) != 2 {
		t.Fatalf("endpoints = %#v, want the stored one and the orphan's", endpoints)
	}
}

func TestPlanUpdateDetectsGatewayEndpointChanges(t *testing.T) {
	for _, tc := range []struct {
		name, storedPath string
		wantRefresh      bool
	}{
		{"unchanged", "/orders", false},
		{"changed path", "/old-orders", true},
		{"missing endpoint", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			g := gateway(filepath.Join(ws, "order-gateway"), "order-gateway", "order-placed")
			g.Values[template.KeyEndpointPath] = "/orders"
			if err := template.WriteScaffold(g.Path, g.Scaffold); err != nil {
				t.Fatal(err)
			}
			model, err := Assemble([]template.ScaffoldEntry{g}, discardWarnf)
			if err != nil {
				t.Fatal(err)
			}
			model.UsePubsub("order-flow-pubsub")
			endpoints := []any{}
			if tc.storedPath != "" {
				endpoints = append(endpoints, map[string]any{"name": g.Values[template.KeyPort], "path": tc.storedPath})
			}
			host := template.Scaffold{Role: template.RoleSystemHost, Values: map[string]any{
				"name": "order-flow", "components": []any{ComponentEntry(model.Components[0])}, "endpoints": endpoints,
			}}
			if err := template.WriteScaffold(filepath.Join(ws, "order-flow"), host); err != nil {
				t.Fatal(err)
			}
			plan, err := planUpdate(UpdateOptions{StartDir: ws, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(plan.refreshed) > 0; got != tc.wantRefresh {
				t.Fatalf("refresh = %v, want %v", got, tc.wantRefresh)
			}
		})
	}
}

func TestMergeWiringRefreshesGatewayPathWithoutMutatingBaseline(t *testing.T) {
	stored := map[string]any{"name": "orders-endpoint", "path": "/old-orders", "note": "kept"}
	plan := &updatePlan{
		baseline:  map[string]any{"endpoints": []any{stored, map[string]any{"name": "absent-endpoint", "path": "/absent"}}},
		refreshed: []Component{{Endpoint: "orders-endpoint", EndpointPath: "/orders"}},
	}
	merged := map[string]any{}
	if err := mergeWiring(plan, merged); err != nil {
		t.Fatal(err)
	}
	endpoints := merged["endpoints"].([]any)
	if got := endpoints[0].(map[string]any); got["path"] != "/orders" || got["note"] != "kept" {
		t.Fatalf("endpoint = %#v", got)
	}
	if stored["path"] != "/old-orders" {
		t.Fatal("baseline mutated")
	}
	if len(endpoints) != 2 || endpoints[1].(map[string]any)["path"] != "/absent" {
		t.Fatalf("endpoints = %#v", endpoints)
	}
}

// A new component's external port reaches the stored list with its kind, and a
// refreshed scaffold that now reads a database turns its stored port external.
func TestMergeWiringRecordsAndRefreshesThePortKind(t *testing.T) {
	plan := &updatePlan{
		hostDir: "host",
		baseline: map[string]any{
			"components": []any{},
			"topics":     []any{},
			"ports":      []any{map[string]any{"name": "order-source"}},
			"messages":   []any{},
		},
		orphans: []Component{{
			AppID: "campaign-extractor", Kind: template.BlockKindExtractor,
			Port: "campaign-source", Ports: []string{"campaign-source"},
			PortKinds: map[string]string{"campaign-source": template.PortKindExternal},
		}},
		refreshed: []Component{{
			AppID: "order-extractor", Kind: template.BlockKindExtractor,
			Port: "order-source", Ports: []string{"order-source"},
			PortKinds: map[string]string{"order-source": template.PortKindExternal},
		}},
	}
	merged := map[string]any{}
	if err := mergeWiring(plan, merged); err != nil {
		t.Fatalf("mergeWiring: %v", err)
	}
	ports := merged["ports"].([]any)
	if len(ports) != 2 {
		t.Fatalf("ports = %#v, want the stored one and the new one", ports)
	}
	for _, p := range ports {
		if p.(map[string]any)["kind"] != template.PortKindExternal {
			t.Errorf("port = %#v, want kind external", p)
		}
	}
	if _, ok := plan.baseline["ports"].([]any)[0].(map[string]any)["kind"]; ok {
		t.Error("the baseline changed; it must keep describing the last render")
	}
}

// sys update adds a transactional block's ports each with its own kind: the
// external source and the binding destination are told apart.
func TestMergeWiringRecordsTransactionalPortKinds(t *testing.T) {
	plan := &updatePlan{
		hostDir: "host",
		baseline: map[string]any{
			"components": []any{},
			"topics":     []any{},
			"ports":      []any{},
			"messages":   []any{},
		},
		orphans: []Component{{
			AppID: "order-sync", Kind: template.BlockKindTransactional,
			Ports:     []string{"order-sync-source", "order-sync-destination"},
			PortKinds: map[string]string{"order-sync-source": template.PortKindExternal},
		}},
	}
	merged := map[string]any{}
	if err := mergeWiring(plan, merged); err != nil {
		t.Fatalf("mergeWiring: %v", err)
	}
	ports := merged["ports"].([]any)
	if len(ports) != 2 {
		t.Fatalf("ports = %#v, want source and destination", ports)
	}
	kinds := map[string]any{}
	for _, p := range ports {
		m := p.(map[string]any)
		kinds[m["name"].(string)] = m["kind"]
	}
	if kinds["order-sync-source"] != template.PortKindExternal {
		t.Errorf("source kind = %v, want external", kinds["order-sync-source"])
	}
	if kind, ok := kinds["order-sync-destination"]; ok && kind != nil {
		t.Errorf("destination kind = %v, want none for a binding port", kind)
	}
}
