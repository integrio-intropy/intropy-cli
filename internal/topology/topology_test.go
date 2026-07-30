package topology

import (
	"strings"
	"testing"
)

const validRecord = `{
  "apiVersion": "topology.intropy.io/v1",
  "kind": "SystemTopology",
  "system": "distribution",
  "components": [
    {
      "name": "extractor",
      "kind": "extractor",
      "subscribes": [],
      "publishes": [
        {"port": "b2c", "pubsub": "price-pubsub", "topic": "price-b2c"},
        {"port": "b2b", "pubsub": "price-pubsub", "topic": "price-b2b"}
      ],
      "connectors": [{"connector": "price-master", "direction": "in"}]
    },
    {
      "name": "erp-loader",
      "kind": "loader",
      "subscribes": [{"pubsub": "price-pubsub", "topic": "price-b2b"}],
      "connectors": [{"connector": "erp", "direction": "out"}]
    }
  ],
  "topics": [
    {"pubsub": "price-pubsub", "topic": "price-b2b", "contract": "Price.Contracts.B2BPrice",
     "publishers": ["extractor"], "subscribers": ["erp-loader"]},
    {"pubsub": "price-pubsub", "topic": "price-b2c", "contract": "Price.Contracts.B2CPrice",
     "publishers": ["extractor"], "subscribers": ["wms-loader"]}
  ],
  "connectors": [
    {"name": "erp", "externalSystem": "erp",
     "transport": {"type": "http", "supportsInput": false, "supportsOutput": true},
     "directions": ["out"], "usedBy": ["erp-loader"]},
    {"name": "price-master", "externalSystem": "price-master",
     "transport": {"type": "sftp", "supportsInput": true, "supportsOutput": true},
     "directions": ["in"], "usedBy": ["extractor"]}
  ],
  "contracts": [
    {"name": "Price.Contracts.B2BPrice", "kind": "event", "shortName": "B2BPrice",
     "mediaType": "application/json", "fingerprint": "sha256:9f2a41c803de11ab",
     "schema": {"type": "object",
                "properties": {"sku": {"type": "string"}},
                "required": ["sku"]}}
  ]
}`

func TestDecodeValid(t *testing.T) {
	got, err := Decode(strings.NewReader(validRecord))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.System != "distribution" {
		t.Errorf("system = %q, want %q", got.System, "distribution")
	}
	if got.Kind != Kind {
		t.Errorf("kind = %q, want %q", got.Kind, Kind)
	}
	if len(got.Components) != 2 || len(got.Topics) != 2 || len(got.Connectors) != 2 {
		t.Fatalf("entity counts = %d/%d/%d, want 2/2/2",
			len(got.Components), len(got.Topics), len(got.Connectors))
	}
	if c := got.Components[0]; c.Kind != "extractor" || len(c.Publishes) != 2 {
		t.Fatalf("extractor = %+v", c)
	}
	if p := got.Components[0].Publishes[0]; p.Port != "b2c" || p.PubSub != "price-pubsub" || p.Topic != "price-b2c" {
		t.Errorf("publish = %+v", p)
	}
	if u := got.Components[0].Connectors[0]; u.Connector != "price-master" || u.Direction != "in" {
		t.Errorf("connector use = %+v", u)
	}
	if s := got.Components[1].Subscribes[0]; s.PubSub != "price-pubsub" || s.Topic != "price-b2b" {
		t.Errorf("subscribe = %+v", s)
	}
	if tp := got.Topics[0]; tp.Topic != "price-b2b" || tp.Contract != "Price.Contracts.B2BPrice" {
		t.Errorf("topic = %+v", tp)
	}
	if cn := got.Connectors[1]; cn.Name != "price-master" || cn.Transport.Type != "sftp" ||
		!cn.Transport.SupportsInput || !cn.Transport.SupportsOutput {
		t.Errorf("connector = %+v", cn)
	}
	if len(got.Contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(got.Contracts))
	}
	if c := got.Contracts[0]; c.Name != "Price.Contracts.B2BPrice" || c.Kind != "event" ||
		c.ShortName != "B2BPrice" || c.Fingerprint != "sha256:9f2a41c803de11ab" {
		t.Errorf("contract = %+v", c)
	}
	// The schema is passed through verbatim, not interpreted.
	if s := string(got.Contracts[0].Schema); !strings.Contains(s, `"required"`) {
		t.Errorf("schema not preserved: %s", s)
	}
}

// A connector that declares a deployed transport decodes it alongside its
// local transport; the two are independent.
func TestDecodeConnectorDeployedTransport(t *testing.T) {
	got, err := Decode(strings.NewReader(`{
		"apiVersion": "topology.intropy.io/v1",
		"system": "x",
		"connectors": [
			{"name": "erp",
			 "transport": {"type": "file", "supportsInput": true, "supportsOutput": true},
			 "deployedTransport": {"type": "sftp", "supportsInput": false, "supportsOutput": true}}
		]
	}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	c := got.Connectors[0]
	if c.Transport.Type != "file" {
		t.Errorf("local transport = %q, want %q", c.Transport.Type, "file")
	}
	if c.DeployedTransport == nil {
		t.Fatal("DeployedTransport = nil, want sftp")
	}
	if c.DeployedTransport.Type != "sftp" ||
		c.DeployedTransport.SupportsInput ||
		!c.DeployedTransport.SupportsOutput {
		t.Errorf("DeployedTransport = %+v, want {sftp false true}", c.DeployedTransport)
	}
}

// A connector without a deployedTransport (an older host, or a connector whose
// local transport also applies to deployment) decodes with a nil DeployedTransport.
func TestDecodeConnectorWithoutDeployedTransport(t *testing.T) {
	got, err := Decode(strings.NewReader(`{
		"apiVersion": "topology.intropy.io/v1",
		"system": "x",
		"connectors": [
			{"name": "erp",
			 "transport": {"type": "file", "supportsInput": true, "supportsOutput": true}}
		]
	}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if c := got.Connectors[0]; c.DeployedTransport != nil {
		t.Errorf("DeployedTransport = %+v, want nil", c.DeployedTransport)
	}
}

// A record without contracts[] (an older host) still decodes; the section is
// simply absent.
func TestDecodeWithoutContracts(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"apiVersion": "topology.intropy.io/v1", "system": "x"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Contracts != nil {
		t.Errorf("contracts = %+v, want nil", got.Contracts)
	}
}

func TestDecodeMalformed(t *testing.T) {
	if _, err := Decode(strings.NewReader("{not json")); err == nil ||
		!strings.Contains(err.Error(), "parse topology record") {
		t.Fatalf("err = %v, want parse error", err)
	}
}

// A host that prints build noise before the JSON violates the stdout contract
// and must be rejected, not guessed at.
func TestDecodeLeadingNoiseIsRejected(t *testing.T) {
	if _, err := Decode(strings.NewReader("Restoring packages...\n" + validRecord)); err == nil {
		t.Fatal("expected error for non-JSON prefix, got nil")
	}
}

// An unknown apiVersion is surfaced as an error, not guessed at.
func TestDecodeUnsupportedAPIVersion(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"apiVersion": "topology.intropy.io/v99", "system": "x"}`)); err == nil ||
		!strings.Contains(err.Error(), "unsupported topology apiVersion") {
		t.Fatalf("err = %v, want unsupported apiVersion error", err)
	}
}

// An old-schema record (integer schemaVersion, system as an object) is
// rejected rather than mis-parsed. It fails at unmarshal (system is not a
// string) before the apiVersion check, which is still a rejection — the
// guarantee is that Decode never silently accepts it.
func TestDecodeOldSchemaIsRejected(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"schemaVersion": 1, "system": {"name": "x"}}`)); err == nil {
		t.Fatal("expected error for old-schema record, got nil")
	}
}
