package system

import (
	"errors"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

func entry(path, kind string, values map[string]any) template.ScaffoldEntry {
	return template.ScaffoldEntry{Path: path, Scaffold: template.Scaffold{BlockKind: kind, Values: values}}
}

func component(path, kind, appID string, values map[string]any) template.ScaffoldEntry {
	values[template.KeyAppID] = appID
	return entry(path, kind, values)
}

func extractor(path, appID, publishes string) template.ScaffoldEntry {
	return component(path, template.BlockKindExtractor, appID, map[string]any{
		template.KeyPublishes: publishes,
		template.KeyPort:      appID + "-source",
	})
}

func loader(path, appID, subscribes string) template.ScaffoldEntry {
	return component(path, template.BlockKindLoader, appID, map[string]any{
		template.KeySubscribes: subscribes,
		template.KeyPort:       appID + "-destination",
	})
}

func transactional(path, appID, from, to string) template.ScaffoldEntry {
	return component(path, template.BlockKindTransactional, appID, map[string]any{
		template.KeyFromPort: from,
		template.KeyToPort:   to,
	})
}

func discardWarnf(string, ...any) {}

func TestAssembleMessageFirstHappyPath(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		extractor("order-extractor", "order-extractor", "order-created"),
		loader("order-loader", "order-loader", "order-created"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(model.Components) != 2 {
		t.Fatalf("components = %+v", model.Components)
	}
	if len(model.Topics) != 1 || model.Topics[0].Name != "order-created" || model.Topics[0].Pubsub != template.DefaultPubsub || model.Topics[0].Contract != "OrderCreated" {
		t.Fatalf("topics = %+v, want derived order-created/OrderCreated", model.Topics)
	}
	if len(model.Messages) != 1 || model.Messages[0].Name != "order-created" || model.Messages[0].Contract != "OrderCreated" || model.Messages[0].Publisher != "order-extractor" {
		t.Fatalf("messages = %+v, want order-created/OrderCreated", model.Messages)
	}
	for _, c := range model.Components {
		if c.Topic == nil || c.Topic.Name != "order-created" || c.Topic.Pubsub != template.DefaultPubsub {
			t.Errorf("component %s topic = %+v", c.AppID, c.Topic)
		}
	}
}

func TestAssembleTransactional(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		transactional("erp-sync", "erp-sync", "erp-source", "erp-destination"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(model.Components) != 1 || len(model.Ports) != 2 || len(model.Messages) != 0 || len(model.Topics) != 0 {
		t.Fatalf("model = %+v", model)
	}
}

func TestAssembleErrors(t *testing.T) {
	t.Run("subscriber without producer", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{loader("order-loader", "order-loader", "order-created")}, discardWarnf)
		if err == nil || !strings.Contains(err.Error(), "no component in this workspace publishes") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("old topic contract records fail", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{component("old", template.BlockKindExtractor, "old", map[string]any{
			template.KeyTopic:    "orders",
			template.KeyContract: "Order",
		})}, discardWarnf)
		if err == nil || !strings.Contains(err.Error(), "values.publishes is missing") {
			t.Fatalf("err = %v, want missing publishes", err)
		}
	})

	t.Run("duplicate component", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{
			extractor("a", "same", "a-created"),
			extractor("b", "same", "b-created"),
		}, discardWarnf)
		if err == nil || !strings.Contains(err.Error(), "duplicate component") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("duplicate port", func(t *testing.T) {
		_, err := Assemble([]template.ScaffoldEntry{
			extractor("a", "a", "a-created"),
			component("b", template.BlockKindExtractor, "b", map[string]any{template.KeyPublishes: "b-created", template.KeyPort: "a-source"}),
		}, discardWarnf)
		if err == nil || !strings.Contains(err.Error(), "duplicate port") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAssembleNoComponentsSentinel(t *testing.T) {
	_, err := Assemble(nil, discardWarnf)
	if !errors.Is(err, ErrNoComponents) {
		t.Fatalf("err = %v, want ErrNoComponents", err)
	}
}

func TestAssembleSkipsWithWarnings(t *testing.T) {
	var warnings []string
	_, err := Assemble([]template.ScaffoldEntry{
		{Path: "host", Scaffold: template.Scaffold{Role: template.RoleSystemHost}},
		{Path: "unknown", Scaffold: template.Scaffold{BlockKind: "unknown", Values: map[string]any{template.KeyAppID: "u"}}},
	}, func(format string, args ...any) { warnings = append(warnings, format) })
	if !errors.Is(err, ErrNoComponents) {
		t.Fatalf("err = %v, want ErrNoComponents", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2", warnings)
	}
}

func TestPascalCaseDerivation(t *testing.T) {
	for in, want := range map[string]string{
		"orders":        "Orders",
		"order-created": "OrderCreated",
		"a.b_c":         "ABC",
	} {
		if got := template.PascalCase(in); got != want {
			t.Errorf("PascalCase(%q) = %q, want %q", in, got, want)
		}
	}
}
