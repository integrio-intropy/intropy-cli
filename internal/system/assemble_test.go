package system

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	texttemplate "text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/huandu/xstrings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func extractorEntry(path, appID, topic, contract string) template.ScaffoldEntry {
	return blockEntry(path, template.BlockKindExtractor, map[string]any{
		"appId": appID, "topic": topic, "contract": contract, "pubsub": "pubsub",
	})
}

func loaderEntry(path, appID, topic, contract string) template.ScaffoldEntry {
	return blockEntry(path, template.BlockKindLoader, map[string]any{
		"appId": appID, "topic": topic, "contract": contract, "pubsub": "pubsub",
	})
}

func blockEntry(path, kind string, values map[string]any) template.ScaffoldEntry {
	return template.ScaffoldEntry{Path: path, Scaffold: template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      kind,
		BlockKind:     kind,
		Values:        values,
	}}
}

func sharedEntry(path, name string) template.ScaffoldEntry {
	return template.ScaffoldEntry{Path: path, Scaffold: template.Scaffold{
		SchemaVersion: template.ScaffoldSchemaVersion,
		Template:      "shared-contracts",
		Role:          template.RoleSharedLibrary,
		Values:        map[string]any{"name": name},
	}}
}

func discardWarnf(string, ...any) {}

func TestAssembleHappyPath(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		loaderEntry("order-loader", "order-loader", "orders", "Order"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(model.Components) != 2 {
		t.Fatalf("components = %+v", model.Components)
	}
	if model.Components[0].AppID != "order-extractor" || model.Components[0].Kind != template.BlockKindExtractor {
		t.Errorf("components[0] = %+v", model.Components[0])
	}
	if model.Components[1].AppID != "order-loader" || model.Components[1].Kind != template.BlockKindLoader {
		t.Errorf("components[1] = %+v", model.Components[1])
	}
	if len(model.Topics) != 1 {
		t.Fatalf("shared topic should dedupe, got %+v", model.Topics)
	}
	want := Topic{TopicKey: TopicKey{Pubsub: "pubsub", Name: "orders"}, Contract: "Order"}
	if model.Topics[0] != want {
		t.Errorf("topic = %+v, want %+v", model.Topics[0], want)
	}
	if model.Shared == nil || *model.Shared != (SharedLibrary{Path: "Contracts", Name: "Contracts"}) {
		t.Errorf("shared = %+v", model.Shared)
	}
}

func TestAssembleDistinctPubsubsMakeDistinctTopics(t *testing.T) {
	extra := blockEntry("audit-loader", template.BlockKindLoader, map[string]any{
		"appId": "audit-loader", "topic": "audits", "contract": "Audit", "pubsub": "audit",
	})
	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		extra,
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(model.Topics) != 2 {
		t.Fatalf("topics = %+v", model.Topics)
	}
	if model.Topics[0].Pubsub != "audit" || model.Topics[1].Pubsub != "pubsub" {
		t.Errorf("topics should sort by (pubsub, name): %+v", model.Topics)
	}
}

func connectedEntry(path, kind, appID, port string) template.ScaffoldEntry {
	return blockEntry(path, kind, map[string]any{
		"appId": appID, "topic": "orders", "contract": "Order", "pubsub": "pubsub", "port": port,
	})
}

func TestAssemblePorts(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		connectedEntry("order-loader", template.BlockKindLoader, "order-loader", "order-loader-destination"),
		connectedEntry("order-extractor", template.BlockKindExtractor, "order-extractor", "order-extractor-source"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if model.Components[0].Port != "order-loader-destination" || model.Components[1].Port != "order-extractor-source" {
		t.Errorf("components = %+v", model.Components)
	}
	want := []Port{
		{Name: "order-extractor-source"},
		{Name: "order-loader-destination"},
	}
	if len(model.Ports) != 2 || model.Ports[0] != want[0] || model.Ports[1] != want[1] {
		t.Errorf("ports = %+v, want %+v (sorted by name)", model.Ports, want)
	}
}

func transactionalEntry(path, appID, from, to string) template.ScaffoldEntry {
	return blockEntry(path, template.BlockKindTransactional, map[string]any{
		"appId": appID, "fromPort": from, "toPort": to,
	})
}

func TestAssembleTransactional(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		transactionalEntry("erp-sync", "erp-sync", "erp-source", "erp-destination"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(model.Components) != 2 {
		t.Fatalf("components = %+v", model.Components)
	}
	tx := model.Components[1]
	if tx.Kind != template.BlockKindTransactional {
		t.Errorf("kind = %q", tx.Kind)
	}
	if tx.Topic != nil {
		t.Errorf("transactional component should carry no topic: %+v", tx.Topic)
	}
	if tx.Port != "" {
		t.Errorf("transactional component should not populate the scalar port: %q", tx.Port)
	}
	if len(tx.Ports) != 2 || tx.Ports[0] != "erp-source" || tx.Ports[1] != "erp-destination" {
		t.Errorf("ports = %+v, want [erp-source erp-destination]", tx.Ports)
	}
	// Both ports join the system's port list; the topic count stays at the
	// extractor's one.
	if len(model.Ports) != 2 || len(model.Topics) != 1 {
		t.Errorf("ports = %+v, topics = %+v", model.Ports, model.Topics)
	}
}

func TestAssembleTransactionalErrors(t *testing.T) {
	tests := []struct {
		name    string
		entry   template.ScaffoldEntry
		wantErr string
	}{
		{
			name: "missing fromPort",
			entry: blockEntry("erp-sync", template.BlockKindTransactional, map[string]any{
				"appId": "erp-sync", "toPort": "erp-destination",
			}),
			wantErr: "values.fromPort is missing",
		},
		{
			name: "empty toPort",
			entry: blockEntry("erp-sync", template.BlockKindTransactional, map[string]any{
				"appId": "erp-sync", "fromPort": "erp-source", "toPort": "",
			}),
			wantErr: "values.toPort is empty",
		},
		{
			name:    "port collides with a topic block's",
			entry:   transactionalEntry("erp-sync", "erp-sync", "erp-source", "orders-source"),
			wantErr: `duplicate port "orders-source"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Assemble([]template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				connectedEntry("order-extractor", template.BlockKindExtractor, "order-extractor", "orders-source"),
				tt.entry,
			}, discardWarnf)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestAssembleMissingPortWarnsAndOmits(t *testing.T) {
	var warned bytes.Buffer
	warnf := func(format string, args ...any) {
		fmt.Fprintf(&warned, format+"\n", args...)
	}

	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"), // predates port
	}, warnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if model.Components[0].Port != "" || len(model.Ports) != 0 {
		t.Errorf("component without a port value must stay bare: %+v / %+v", model.Components, model.Ports)
	}
	if !strings.Contains(warned.String(), "order-extractor: scaffold record has no port") {
		t.Errorf("warnings = %s", warned.String())
	}
}

func TestAssembleMissingPubsubDefaults(t *testing.T) {
	e := blockEntry("order-extractor", template.BlockKindExtractor, map[string]any{
		"appId": "order-extractor", "topic": "orders", "contract": "Order",
	})
	model, err := Assemble([]template.ScaffoldEntry{sharedEntry("Contracts", "Contracts"), e}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if model.Topics[0].Pubsub != "pubsub" {
		t.Errorf("pubsub should default to %q, got %q", "pubsub", model.Topics[0].Pubsub)
	}
}

func TestAssembleErrors(t *testing.T) {
	tests := []struct {
		name    string
		entries []template.ScaffoldEntry
		wantErr string
	}{
		{
			name:    "no components",
			entries: []template.ScaffoldEntry{sharedEntry("Contracts", "Contracts")},
			wantErr: "no assemblable integration scaffolds",
		},
		{
			name: "missing contract",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				blockEntry("order-extractor", template.BlockKindExtractor, map[string]any{
					"appId": "order-extractor", "topic": "orders",
				}),
			},
			wantErr: "values.contract is missing",
		},
		{
			name: "non-string topic",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				blockEntry("order-extractor", template.BlockKindExtractor, map[string]any{
					"appId": "order-extractor", "topic": float64(7), "contract": "Order",
				}),
			},
			wantErr: "values.topic has type float64, expected string",
		},
		{
			name: "duplicate app ids",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				extractorEntry("a", "orders", "orders", "Order"),
				loaderEntry("b", "orders", "orders", "Order"),
			},
			wantErr: `duplicate component name "orders"`,
		},
		{
			name: "conflicting contracts",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				extractorEntry("a", "a", "orders", "Order"),
				loaderEntry("b", "b", "orders", "Invoice"),
			},
			wantErr: "conflicting contracts",
		},
		{
			name: "two shared libraries with topics",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				sharedEntry("LegacyModels", "LegacyModels"),
				extractorEntry("a", "a", "orders", "Order"),
			},
			wantErr: "found 2 shared contract projects",
		},
		{
			name: "two shared libraries without topics",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				sharedEntry("LegacyModels", "LegacyModels"),
				transactionalEntry("erp-sync", "erp-sync", "erp-source", "erp-destination"),
			},
			wantErr: "found 2 shared contract projects",
		},
		{
			name: "duplicate port",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				connectedEntry("a", template.BlockKindExtractor, "a", "orders-source"),
				connectedEntry("b", template.BlockKindExtractor, "b", "orders-source"),
			},
			wantErr: `duplicate port "orders-source"`,
		},
		{
			name: "empty port",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				connectedEntry("a", template.BlockKindExtractor, "a", ""),
			},
			wantErr: "values.port is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Assemble(tt.entries, discardWarnf)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestAssembleNoComponentsSentinel(t *testing.T) {
	_, err := Assemble(nil, discardWarnf)
	if !errors.Is(err, ErrNoComponents) {
		t.Errorf("err = %v, want ErrNoComponents", err)
	}
}

func TestAssembleSkipsWithWarnings(t *testing.T) {
	var warned bytes.Buffer
	warnf := func(format string, args ...any) {
		fmt.Fprintf(&warned, format+"\n", args...)
	}

	noKind := template.ScaffoldEntry{Path: "legacy", Scaffold: template.Scaffold{
		Template: "extractor", Values: map[string]any{"appId": "legacy"},
	}}
	host := template.ScaffoldEntry{Path: "old-host", Scaffold: template.Scaffold{
		Template: "system-host", Role: template.RoleSystemHost, Values: map[string]any{},
	}}
	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		noKind,
		host,
		blockEntry("agg", "reconciler", map[string]any{"appId": "agg"}),
	}, warnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(model.Components) != 1 {
		t.Errorf("components = %+v", model.Components)
	}
	for _, want := range []string{
		"skipping legacy: scaffold record has no block kind",
		"skipping old-host: an existing system host",
		`skipping agg: unsupported block kind "reconciler"`,
	} {
		if !strings.Contains(warned.String(), want) {
			t.Errorf("warnings missing %q:\n%s", want, warned.String())
		}
	}
}

func TestAssembleWithoutSharedLibrary(t *testing.T) {
	t.Run("transactional-only system assembles contracts-free", func(t *testing.T) {
		model, err := Assemble([]template.ScaffoldEntry{
			transactionalEntry("erp-sync", "erp-sync", "erp-source", "erp-destination"),
		}, discardWarnf)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if model.Shared != nil {
			t.Errorf("shared = %+v, want nil", model.Shared)
		}
		if len(model.Topics) != 0 || len(model.Ports) != 2 {
			t.Errorf("topics = %+v, ports = %+v", model.Topics, model.Ports)
		}
	})

	t.Run("topic-bearing system assembles; the host template supplies contracts", func(t *testing.T) {
		model, err := Assemble([]template.ScaffoldEntry{
			extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		}, discardWarnf)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if model.Shared != nil {
			t.Errorf("shared = %+v, want nil", model.Shared)
		}
		if len(model.Topics) != 1 {
			t.Errorf("topics = %+v", model.Topics)
		}
	})
}

// The templates derive their kebab-case values with sprig's kebabcase; the
// CLI must derive the same system name from the same input. Locks the
// assumption that sprig delegates to xstrings.ToKebabCase.
func TestKebabParityWithSprig(t *testing.T) {
	kebab := texttemplate.Must(texttemplate.New("k").Funcs(sprig.TxtFuncMap()).Parse(`{{ kebabcase . }}`))
	for _, name := range []string{"OrderFlow", "order-flow", "OrderFlow2", "HTTPGateway"} {
		var buf bytes.Buffer
		if err := kebab.Execute(&buf, name); err != nil {
			t.Fatal(err)
		}
		if got := xstrings.ToKebabCase(name); got != buf.String() {
			t.Errorf("xstrings.ToKebabCase(%q) = %q, sprig kebabcase = %q", name, got, buf.String())
		}
	}
}

func publishesEntry(path, appID, message, contract string) template.ScaffoldEntry {
	values := map[string]any{
		"appId": appID,
		"publishes": map[string]any{
			"message": message,
		},
	}
	if contract != "" {
		values["publishes"].(map[string]any)["contract"] = contract
	}
	return blockEntry(path, template.BlockKindExtractor, values)
}

// AE4: a publisher and a subscriber assemble as one system; the
// subscriber's channel comes from the publisher, and the payload's
// messagegroup names the message with its type.
func TestAssembleMessageBlocksHappyPath(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		publishesEntry("product-sink", "product-sink", "product-exported", "ProductExported"),
		blockEntry("product-loader", template.BlockKindLoader, map[string]any{
			"appId": "product-loader",
			"subscribe": map[string]any{
				"message": "product-exported",
			},
		}),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(model.Messages) != 1 {
		t.Fatalf("messages = %+v", model.Messages)
	}
	msg := model.Messages[0]
	if msg.Name != "product-exported" || msg.Type != "product-exported" {
		t.Errorf("message = %+v, want name/type product-exported", msg)
	}
	if msg.Contract != "ProductExported" {
		t.Errorf("contract = %q, want the transitional .NET type carried", msg.Contract)
	}
	if msg.Publisher != "product-sink" {
		t.Errorf("publisher = %q", msg.Publisher)
	}

	// The subscriber resolved its channel from the producer's default:
	// system pubsub, topic named after the message.
	var loader Component
	for _, c := range model.Components {
		if c.AppID == "product-loader" {
			loader = c
		}
	}
	if loader.Topic == nil || loader.Topic.Name != "product-exported" || loader.Topic.Pubsub != template.DefaultPubsub {
		t.Errorf("loader topic = %+v, want the producer-resolved channel", loader.Topic)
	}

}

// The workload spread across the pubsub default and the message topic is
// the shared channel both halves resolve to.
func TestAssembleMessageBlocksChannelKey(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		publishesEntry("sink", "sink", "m", "M"),
		blockEntry("loader", template.BlockKindLoader, map[string]any{
			"appId": "loader", "subscribe": map[string]any{"message": "m"},
		}),
	}, discardWarnf)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Topics) != 1 || model.Topics[0].TopicKey != (template.TopicKey{Pubsub: template.DefaultPubsub, Name: "m"}) {
		t.Fatalf("topics = %+v, want exactly the resolved channel once", model.Topics)
	}
}

// An external subscribe block carries its own snapshot: it assembles with
// no producing record and never touches a registry (assembly is offline).
func TestAssembleExternalSubscribeIsOffline(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		blockEntry("product-loader", template.BlockKindLoader, map[string]any{
			"appId": "product-loader",
			"subscribe": map[string]any{
				"message":       "io.intropy.maxbo.product.export",
				"pubsub":        "product-distribution-pubsub",
				"topic":         "sbt-test-product-extractor-001",
				"dataschema":    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
				"dataschemaurl": "https://registry.example.com/versions/1",
			},
		}),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(model.Components) != 1 || model.Components[0].Message == nil || !model.Components[0].Message.External {
		t.Fatalf("components = %+v", model.Components)
	}
	if model.Components[0].Topic == nil || *model.Components[0].Topic != (template.TopicKey{Pubsub: "product-distribution-pubsub", Name: "sbt-test-product-extractor-001"}) {
		t.Errorf("topic = %+v, want the snapshot verbatim", model.Components[0].Topic)
	}
	// Registry messages do not enter the internal messagegroup.
	if len(model.Messages) != 0 {
		t.Errorf("messages = %+v, want none for an external-only workspace", model.Messages)
	}
}

// Mixed legacy + new records assemble into one system: the legacy pair on
// its flat keys, the new pair on its blocks, no cross-contamination.
func TestAssembleMixedShapes(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		loaderEntry("order-loader", "order-loader", "orders", "Order"),
		publishesEntry("product-sink", "product-sink", "product-exported", "ProductExported"),
		blockEntry("product-loader", template.BlockKindLoader, map[string]any{
			"appId": "product-loader", "subscribe": map[string]any{"message": "product-exported"},
		}),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(model.Components) != 4 || len(model.Topics) != 2 || len(model.Messages) != 2 {
		t.Fatalf("components=%d topics=%d messages=%d", len(model.Components), len(model.Topics), len(model.Messages))
	}
}

// A block-shaped subscriber whose message names a legacy topic resolves
// its channel from the legacy producer: the topic name was the only
// message identity the flat vocabulary had, and the two spaces must agree.
func TestAssembleMixedLegacyProducerResolvesNewSubscriber(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"),
		blockEntry("order-loader", template.BlockKindLoader, map[string]any{
			"appId": "order-loader", "subscribe": map[string]any{"message": "orders"},
		}),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	for _, c := range model.Components {
		if c.AppID == "order-loader" && (c.Topic == nil || *c.Topic != (template.TopicKey{Pubsub: "pubsub", Name: "orders"})) {
			t.Errorf("loader topic = %+v, want the legacy producer's channel", c.Topic)
		}
	}
}

func TestAssembleMessageErrors(t *testing.T) {
	t.Run("internal subscriber names unpublished message", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{
			blockEntry("orphan-loader", template.BlockKindLoader, map[string]any{
				"appId": "orphan-loader", "subscribe": map[string]any{"message": "nowhere-exported"},
			}),
		}, discardWarnf)
		if err == nil {
			t.Fatal("expected a hard error")
		}
		for _, want := range []string{"nowhere-exported", "orphan-loader"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should name %q", err, want)
			}
		}
	})

	t.Run("two producers, same message, different channels", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{
			publishesEntry("sink-a", "sink-a", "m", "M"),
			blockEntry("sink-b", template.BlockKindExtractor, map[string]any{
				"appId":     "sink-b",
				"publishes": map[string]any{"message": "m", "contract": "M2"},
			}),
		}, discardWarnf)
		if err == nil {
			t.Fatal("expected a conflict error")
		}
		for _, want := range []string{"m", "sink-a", "sink-b"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should name %q", err, want)
			}
		}
	})

	t.Run("both blocks in one record", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{
			blockEntry("both", template.BlockKindExtractor, map[string]any{
				"appId":     "both",
				"publishes": map[string]any{"message": "m"},
				"subscribe": map[string]any{"message": "m"},
			}),
		}, discardWarnf)
		if err == nil || !strings.Contains(err.Error(), "both a subscribe and a publishes block") {
			t.Errorf("err = %v, want the one-direction error", err)
		}
	})

	t.Run("mistyped block field fails the record", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{
			blockEntry("broken", template.BlockKindLoader, map[string]any{
				"appId": "broken", "subscribe": map[string]any{"message": 7.0},
			}),
		}, discardWarnf)
		if err == nil || !strings.Contains(err.Error(), "subscribe.message") {
			t.Errorf("err = %v, want an error naming subscribe.message", err)
		}
	})
}

// The legacy topic-conflict rule still fires unchanged: two differing
// contracts on one topic key is a conflict, contracts omitted by new
// records are not.
func TestAssembleLegacyTopicConflictStillFires(t *testing.T) {
	_, err := Assemble([]template.ScaffoldEntry{
		extractorEntry("a", "a", "orders", "Order"),
		loaderEntry("b", "b", "orders", "OrderV2"),
	}, discardWarnf)
	if err == nil || !strings.Contains(err.Error(), "conflicting contracts") {
		t.Fatalf("err = %v, want the topic-conflict error", err)
	}

	t.Run("empty contracts do not conflict", func(t *testing.T) {
		// Producer publishes m with contract; external snapshot names the
		// same channel without a contract — they agree silently.
		if _, err := Assemble([]template.ScaffoldEntry{
			publishesEntry("sink", "sink", "m", "M"),
			blockEntry("loader", template.BlockKindLoader, map[string]any{
				"appId": "loader",
				"subscribe": map[string]any{
					"message": "external", "pubsub": template.DefaultPubsub, "topic": "m",
				},
			}),
		}, discardWarnf); err != nil {
			t.Errorf("Assemble: %v", err)
		}
	})
}

func snapshotPublishesEntry(path, appID string) template.ScaffoldEntry {
	return blockEntry(path, template.BlockKindExtractor, map[string]any{
		"appId": appID,
		"publishes": map[string]any{
			"message":       "io.intropy.maxbo.product.export",
			"contract":      "ProductExported",
			"pubsub":        "product-distribution-pubsub",
			"topic":         "sbt-test-product-extractor-001",
			"dataschema":    "/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1",
			"dataschemaurl": "https://registry.example.com/schemagroups/io.intropy.maxbo.product/schemas/product-export.v1/versions/1",
		},
	})
}

// AE4: a registry-resolved publication and an internal subscriber of the
// same message assemble onto the recorded channel, and the external
// publication stays out of the internal messagegroup.
func TestAssembleExternalPublishResolvesInternalSubscriber(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		snapshotPublishesEntry("erp-extractor", "erp-extractor"),
		blockEntry("loader", template.BlockKindLoader, map[string]any{
			"appId": "loader",
			"subscribe": map[string]any{
				"message": "io.intropy.maxbo.product.export",
			},
		}),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var extractor, loader Component
	for _, c := range model.Components {
		switch c.AppID {
		case "erp-extractor":
			extractor = c
		case "loader":
			loader = c
		}
	}
	if extractor.Topic == nil || *extractor.Topic != (template.TopicKey{Pubsub: "product-distribution-pubsub", Name: "sbt-test-product-extractor-001"}) {
		t.Errorf("publisher topic = %+v, want the recorded snapshot verbatim", extractor.Topic)
	}
	if loader.Topic == nil || *loader.Topic != (template.TopicKey{Pubsub: "product-distribution-pubsub", Name: "sbt-test-product-extractor-001"}) {
		t.Errorf("subscriber topic = %+v, want the publisher's recorded channel", loader.Topic)
	}
	// The registry serves this message's definition; the internal
	// messagegroup must not duplicate it.
	if len(model.Messages) != 0 {
		t.Errorf("messages = %+v, want none for an externally-published message", model.Messages)
	}
}

// A snapshot publishes block naming a topic but no pubsub is a broken
// record: assembly reports it, the reader alone never validated the pair.
func TestAssembleExternalPublishRequiresPubsub(t *testing.T) {
	_, err := Assemble([]template.ScaffoldEntry{
		blockEntry("erp-extractor", template.BlockKindExtractor, map[string]any{
			"appId": "erp-extractor",
			"publishes": map[string]any{
				"message": "io.intropy.maxbo.product.export",
				"topic":   "sbt-test-product-extractor-001",
			},
		}),
	}, discardWarnf)
	if err == nil || !strings.Contains(err.Error(), "pubsub") {
		t.Errorf("err = %v, want the pubsub-required error", err)
	}
}

// Two producers of one message on different channels is the conflict
// assembly has always refused; a snapshot publication must join that
// comparison with its real channel, not a synthetic one.
func TestAssembleExternalPublishChannelConflict(t *testing.T) {
	a := snapshotPublishesEntry("extractor-a", "extractor-a")
	a.Values["publishes"].(map[string]any)["topic"] = "sbt-test-extractor-a"
	b := snapshotPublishesEntry("extractor-b", "extractor-b")
	b.Values["publishes"].(map[string]any)["pubsub"] = "erp-distribution-pubsub"
	b.Values["publishes"].(map[string]any)["topic"] = "sbt-test-extractor-b"

	_, err := Assemble([]template.ScaffoldEntry{a, b}, discardWarnf)
	if err == nil || !strings.Contains(err.Error(), "conflicting channels") {
		t.Errorf("err = %v, want the conflicting-channels error", err)
	}
}

// The mirror half-wire: a recorded pubsub without a topic is a channel the
// system cannot assemble. Silently dropping the recorded half would
// fabricate a different channel under the internal default.
func TestAssembleExternalPublishRequiresTopic(t *testing.T) {
	_, err := Assemble([]template.ScaffoldEntry{
		blockEntry("erp-extractor", template.BlockKindExtractor, map[string]any{
			"appId": "erp-extractor",
			"publishes": map[string]any{
				"message": "io.intropy.maxbo.product.export",
				"pubsub":  "product-distribution-pubsub",
			},
		}),
	}, discardWarnf)
	if err == nil || !strings.Contains(err.Error(), "topic") {
		t.Errorf("err = %v, want the topic-required error", err)
	}
}

// A snapshot publication and a legacy-record publication declaring
// different contracts on the same real channel conflict; the snapshot's
// contract must join that comparison, not vanish with an unset topic
// contract.
func TestAssembleExternalPublishContractConflict(t *testing.T) {
	_, err := Assemble([]template.ScaffoldEntry{
		snapshotPublishesEntry("extractor-a", "extractor-a"),
		blockEntry("extractor-b", template.BlockKindExtractor, map[string]any{
			"appId":    "extractor-b",
			"pubsub":   "product-distribution-pubsub",
			"topic":    "sbt-test-product-extractor-001",
			"contract": "OtherContract",
		}),
	}, discardWarnf)
	if err == nil || !strings.Contains(err.Error(), "conflicting contracts") {
		t.Errorf("err = %v, want the conflicting-contracts error", err)
	}
}
