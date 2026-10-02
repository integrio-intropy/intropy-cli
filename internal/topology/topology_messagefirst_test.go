package topology

// WS-1.3 U4: the message-first host's graph document decodes, keeps
// messagegroups[] opaquely, and round-trips byte-stably for the consumers
// that re-marshal it (the dashboard's /api/topology). The fixture is real
// output from a message-first host build (see decodeMessageFirstFixture).
import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// decodeMessageFirstFixture reads C5-conformant graph output captured from a
// real message-first host build: intropy-topology@feature/message-first-topology,
// examples/OrderFlow.SystemHost (rendered by the message-first templates),
// `dotnet run -- graph`, against the 1.1.0-rc.1 topology prerelease.
func decodeMessageFirstFixture(t *testing.T) ([]byte, *Topology) {
	t.Helper()
	raw, err := os.ReadFile("testdata/messagefirst_system_graph.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return raw, got
}

// A message-first graph decodes with the additive messagegroups[] section
// carried opaquely, and the frozen C5 sections (topics[] with
// publishers/subscribers, ports[]) intact for deploy-side consumers.
func TestDecodeMessageFirstGraph(t *testing.T) {
	_, got := decodeMessageFirstFixture(t)

	if got.APIVersion != APIVersion {
		t.Errorf("apiVersion = %q, want %q", got.APIVersion, APIVersion)
	}
	if len(got.Topics) != 1 || got.Topics[0].Topic != "orders" {
		t.Fatalf("topics = %+v, want the message-named topic", got.Topics)
	}
	if got.Topics[0].Contract != "Contracts.Order" {
		t.Errorf("topic contract = %q, want Contracts.Order", got.Topics[0].Contract)
	}
	if len(got.Topics[0].Publishers) != 1 || got.Topics[0].Publishers[0] != "order-extractor" {
		t.Errorf("pub/sub topology drifted: publishers = %v", got.Topics[0].Publishers)
	}
	subscribed := false
	for _, c := range got.Components {
		if c.Name == "order-loader" && len(c.Subscribes) == 1 && c.Subscribes[0].Topic == "orders" {
			subscribed = true
		}
	}
	if !subscribed {
		t.Errorf("no loader subscription found: %+v", got.Components)
	}

	var group struct {
		Name     string `json:"name"`
		Messages []struct {
			Name string `json:"name"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(got.MessageGroups[0], &group); err != nil {
		t.Fatalf("messagegroups is not opaque JSON: %v", err)
	}
	if group.Name != "order-flow" || len(group.Messages) != 1 || group.Messages[0].Name != "orders" {
		t.Errorf("messagegroup = %+v", group)
	}
}

// Decode → re-encode → decode → re-encode is byte-stable, and messagegroups
// survives the re-encode verbatim — the contract the dashboard's JSON
// re-marshal of topology.Entry relies on.
func TestDecodeMessageFirstGraphRoundTrip(t *testing.T) {
	raw, first := decodeMessageFirstFixture(t)

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Decode(bytes.NewReader(firstJSON))
	if err != nil {
		t.Fatalf("decode of the re-encoded record: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Errorf("re-encode is not byte-stable across passes:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}

	var source, roundTripped struct {
		MessageGroups []json.RawMessage `json:"messagegroups"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(firstJSON, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if len(source.MessageGroups) != 1 || len(roundTripped.MessageGroups) != 1 {
		t.Fatalf("messagegroups lost in the round trip: %d → %d", len(source.MessageGroups), len(roundTripped.MessageGroups))
	}
	if !bytes.Equal(compactJSON(t, source.MessageGroups[0]), compactJSON(t, roundTripped.MessageGroups[0])) {
		t.Errorf("messagegroups content changed in the round trip:\n%s\n%s", source.MessageGroups[0], roundTripped.MessageGroups[0])
	}
}

func compactJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
