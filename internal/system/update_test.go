package system

import (
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
