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
	want := Topic{TopicKey: TopicKey{Pubsub: "pubsub", Name: "orders"}, Contract: "Order", Field: "Orders"}
	if model.Topics[0] != want {
		t.Errorf("topic = %+v, want %+v", model.Topics[0], want)
	}
	if model.Shared != (SharedLibrary{Path: "Contracts", Name: "Contracts"}) {
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

func connectedEntry(path, kind, appID, connector string) template.ScaffoldEntry {
	return blockEntry(path, kind, map[string]any{
		"appId": appID, "topic": "orders", "contract": "Order", "pubsub": "pubsub", "connector": connector,
	})
}

func TestAssembleConnectors(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		connectedEntry("order-loader", template.BlockKindLoader, "order-loader", "order-loader-destination"),
		connectedEntry("order-extractor", template.BlockKindExtractor, "order-extractor", "order-extractor-source"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if model.Components[0].Connector != "order-loader-destination" || model.Components[1].Connector != "order-extractor-source" {
		t.Errorf("components = %+v", model.Components)
	}
	want := []Connector{
		{Name: "order-extractor-source", Field: "OrderExtractorSource"},
		{Name: "order-loader-destination", Field: "OrderLoaderDestination"},
	}
	if len(model.Connectors) != 2 || model.Connectors[0] != want[0] || model.Connectors[1] != want[1] {
		t.Errorf("connectors = %+v, want %+v (sorted by name)", model.Connectors, want)
	}
}

func TestAssembleMissingConnectorWarnsAndOmits(t *testing.T) {
	var warned bytes.Buffer
	warnf := func(format string, args ...any) {
		fmt.Fprintf(&warned, format+"\n", args...)
	}

	model, err := Assemble([]template.ScaffoldEntry{
		sharedEntry("Contracts", "Contracts"),
		extractorEntry("order-extractor", "order-extractor", "orders", "Order"), // predates connector
	}, warnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if model.Components[0].Connector != "" || len(model.Connectors) != 0 {
		t.Errorf("component without a connector value must stay bare: %+v / %+v", model.Components, model.Connectors)
	}
	if !strings.Contains(warned.String(), "order-extractor: scaffold record has no connector") {
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
			name: "no shared library",
			entries: []template.ScaffoldEntry{
				extractorEntry("a", "a", "orders", "Order"),
			},
			wantErr: "no shared contracts project found",
		},
		{
			name: "two shared libraries",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				sharedEntry("LegacyModels", "LegacyModels"),
				extractorEntry("a", "a", "orders", "Order"),
			},
			wantErr: "found 2 shared contract projects",
		},
		{
			name: "duplicate connector",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				connectedEntry("a", template.BlockKindExtractor, "a", "orders-source"),
				connectedEntry("b", template.BlockKindExtractor, "b", "orders-source"),
			},
			wantErr: `duplicate connector "orders-source"`,
		},
		{
			name: "empty connector",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				connectedEntry("a", template.BlockKindExtractor, "a", ""),
			},
			wantErr: "values.connector is empty",
		},
		{
			name: "connector field collision",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				connectedEntry("a", template.BlockKindExtractor, "a", "erp-source"),
				connectedEntry("b", template.BlockKindLoader, "b", "erp.source"),
			},
			wantErr: "both map to field ErpSource in Connectors.cs",
		},
		{
			name: "topic field collision",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				extractorEntry("a", "a", "order-events", "Order"),
				loaderEntry("b", "b", "order.events", "Order"),
			},
			wantErr: "both map to field OrderEvents",
		},
		{
			name: "same topic name across pubsubs collides",
			entries: []template.ScaffoldEntry{
				sharedEntry("Contracts", "Contracts"),
				extractorEntry("a", "a", "orders", "Order"),
				blockEntry("b", template.BlockKindLoader, map[string]any{
					"appId": "b", "topic": "orders", "contract": "Order", "pubsub": "audit",
				}),
			},
			wantErr: "both map to field Orders",
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

func TestPascalIdent(t *testing.T) {
	tests := map[string]string{
		"orders":       "Orders",
		"order-events": "OrderEvents",
		"order.events": "OrderEvents",
		"stock":        "Stock",
	}
	for in, want := range tests {
		if got := pascalIdent(in); got != want {
			t.Errorf("pascalIdent(%q) = %q, want %q", in, got, want)
		}
	}
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
