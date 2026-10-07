package system

import (
	"reflect"
	"strings"
	"testing"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// channeled is an extractor publishing onto a topic other than its message's
// name — the override that lets several messages share one topic.
func channeled(appID, publishes, channel string) template.ScaffoldEntry {
	e := extractor(appID, appID, publishes)
	e.Values[template.KeyChannel] = channel
	return e
}

func routingLoader(appID, channel, def string, routes ...template.Route) template.ScaffoldEntry {
	list := make([]any, len(routes))
	for i, r := range routes {
		m := map[string]any{template.KeyMessage: r.Message}
		if r.When != "" {
			m[template.KeyWhen] = r.When
		}
		list[i] = m
	}
	values := map[string]any{
		template.KeyRoutes: list,
		template.KeyPort:   appID + "-destination",
	}
	if channel != "" {
		values[template.KeyChannel] = channel
	}
	if def != "" {
		values[template.KeyDefault] = def
	}
	return component(appID, template.BlockKindLoader, appID, values)
}

// A loader routes several messages its producers publish onto one shared
// topic: it consumes that topic, and the payload carries its routes, its
// default and every message's channel.
func TestAssembleRoutesMessagesSharingATopic(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		channeled("order-extractor", "order-placed", "orders"),
		channeled("cancellation-extractor", "order-cancelled", "orders"),
		routingLoader("order-loader", "orders", template.DefaultIgnore,
			template.Route{Message: "order-placed"},
			template.Route{Message: "order-cancelled", When: "event.data.reason != 'fraud-review'"}),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(model.Topics) != 1 || model.Topics[0].Name != "orders" {
		t.Fatalf("topics = %+v, want the one shared topic", model.Topics)
	}
	loaderEntry := ComponentEntry(model.Components[2])
	if got := loaderEntry[template.KeyTopic].(map[string]any)[template.KeyName]; got != "orders" {
		t.Errorf("loader topic = %v", got)
	}
	wantRoutes := []any{
		map[string]any{template.KeyMessage: "order-placed"},
		map[string]any{template.KeyMessage: "order-cancelled", template.KeyWhen: "event.data.reason != 'fraud-review'"},
	}
	if !reflect.DeepEqual(loaderEntry[template.KeyRoutes], wantRoutes) || loaderEntry[template.KeyDefault] != template.DefaultIgnore {
		t.Errorf("loader entry = %#v", loaderEntry)
	}
	for _, m := range model.Messages {
		if entry := MessageEntry(m); entry[template.KeyTopic] != "orders" || entry[template.KeyPubsub] != template.DefaultPubsub {
			t.Errorf("message entry = %#v, want its channel", entry)
		}
	}
}

// A loader cannot route a message published on another topic than the one
// it consumes; the error names both topics and how to fix it.
func TestAssembleRefusesARoutedMessageOnAnotherTopic(t *testing.T) {
	_, err := Assemble([]template.ScaffoldEntry{
		channeled("order-extractor", "order-placed", "orders"),
		extractor("cancellation-extractor", "cancellation-extractor", "order-cancelled"),
		routingLoader("order-loader", "orders", "",
			template.Route{Message: "order-placed"},
			template.Route{Message: "order-cancelled"}),
	}, discardWarnf)
	if err == nil {
		t.Fatal("Assemble accepted routes over two topics")
	}
	for _, want := range []string{`"order-cancelled"`, `topic "order-cancelled"`, `consumes topic "orders"`, "values.channel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %s", err, want)
		}
	}
}

// Without a recorded channel a loader consumes its first route's producer's
// topic; a scalar subscribes record is one route that dead-letters the rest.
func TestAssembleScalarSubscriberIsOneDeadLetteringRoute(t *testing.T) {
	model, err := Assemble([]template.ScaffoldEntry{
		extractor("order-extractor", "order-extractor", "order-created"),
		loader("order-loader", "order-loader", "order-created"),
	}, discardWarnf)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	l := model.Components[1]
	if l.Topic == nil || l.Topic.Name != "order-created" || l.Default != template.DefaultDeadLetter ||
		!reflect.DeepEqual(l.Routes, []template.Route{{Message: "order-created"}}) {
		t.Errorf("loader = %+v", l)
	}
}

func TestAssembleRefusesAnUnknownDefault(t *testing.T) {
	_, err := Assemble([]template.ScaffoldEntry{
		extractor("order-extractor", "order-extractor", "order-created"),
		routingLoader("order-loader", "", "drop", template.Route{Message: "order-created"}),
	}, discardWarnf)
	if err == nil || !strings.Contains(err.Error(), `values.default is "drop"`) {
		t.Errorf("err = %v", err)
	}
}
