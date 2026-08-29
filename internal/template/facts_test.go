package template

import (
	"testing"
)

func factEntry(kind string, values map[string]any) WorkspaceFactEntry {
	return WorkspaceFactEntry{BlockKind: kind, Values: values}
}

func TestBuildWorkspaceFacts(t *testing.T) {
	t.Run("extractor and loader sharing a topic contribute one key, one contract, both ports", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "order-extractor", "topic": "orders", "contract": "Order", "pubsub": "pubsub", "port": "erp",
			}),
			factEntry(BlockKindLoader, map[string]any{
				"appId": "audit-loader", "topic": "orders", "contract": "Order", "pubsub": "pubsub", "port": "warehouse",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 1 || facts.TopicKeys[0] != (TopicKey{Pubsub: "pubsub", Name: "orders"}) {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
		if got, ok := facts.ContractFor(TopicKey{Pubsub: "pubsub", Name: "orders"}); !ok || got != "Order" {
			t.Fatalf("contract = %q, %v", got, ok)
		}
		if len(facts.Ports) != 2 || facts.Ports[0] != "erp" || facts.Ports[1] != "warehouse" {
			t.Fatalf("ports = %+v", facts.Ports)
		}
	})

	t.Run("transactional record contributes both ports and no topic", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindTransactional, map[string]any{
				"appId": "sync", "fromPort": "crm", "toPort": "erp",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 0 {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
		if len(facts.Ports) != 2 || facts.Ports[0] != "crm" || facts.Ports[1] != "erp" {
			t.Fatalf("ports = %+v", facts.Ports)
		}
	})

	t.Run("conflicting contracts on one topic key demote the contract but keep the key", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "a", "topic": "orders", "contract": "Order",
			}),
			factEntry(BlockKindLoader, map[string]any{
				"appId": "b", "topic": "orders", "contract": "OrderV2",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 1 {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
		if got, ok := facts.ContractFor(facts.TopicKeys[0]); ok || got != "" {
			t.Fatalf("conflicted contract should demote, got %q, %v", got, ok)
		}
	})

	t.Run("records missing wiring values are skipped, others still contribute", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindLoader, map[string]any{"appId": "bare"}),
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "partial", "topic": "audits", "contract": "Audit",
			}),
			factEntry(BlockKindLoader, map[string]any{
				"appId": "nonstring", "topic": 7, "contract": "Order",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 1 || facts.TopicKeys[0].Name != "audits" {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
	})

	t.Run("pubsub defaults to pubsub for records that predate the value", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "old", "topic": "orders", "contract": "Order",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 1 || facts.TopicKeys[0].Pubsub != "pubsub" {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
	})

	t.Run("records without a block kind contribute nothing", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry("", map[string]any{"name": "Acme.Models"}),
			factEntry("future-kind", map[string]any{"topic": "orders", "contract": "Order"}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 0 || len(facts.Ports) != 0 {
			t.Fatalf("facts = %+v", facts)
		}
	})

	t.Run("topics sort by pubsub then topic, ports lexicographic", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "a", "topic": "orders", "contract": "Order", "pubsub": "z-pubsub", "port": "zeta",
			}),
			factEntry(BlockKindLoader, map[string]any{
				"appId": "b", "topic": "audits", "contract": "Audit", "pubsub": "a-pubsub", "port": "alpha",
			}),
			factEntry(BlockKindLoader, map[string]any{
				"appId": "c", "topic": "billing", "contract": "Invoice", "pubsub": "a-pubsub", "port": "mid",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		wantTopics := []TopicKey{
			{Pubsub: "a-pubsub", Name: "audits"},
			{Pubsub: "a-pubsub", Name: "billing"},
			{Pubsub: "z-pubsub", Name: "orders"},
		}
		if len(facts.TopicKeys) != 3 {
			t.Fatalf("topic keys = %+v", facts.TopicKeys)
		}
		for i, want := range wantTopics {
			if facts.TopicKeys[i] != want {
				t.Fatalf("topic keys[%d] = %+v, want %+v (all: %+v)", i, facts.TopicKeys[i], want, facts.TopicKeys)
			}
		}
		wantPorts := []string{"alpha", "mid", "zeta"}
		for i, want := range wantPorts {
			if facts.Ports[i] != want {
				t.Fatalf("ports = %+v, want %v", facts.Ports, wantPorts)
			}
		}
	})

	t.Run("empty input yields an empty index", func(t *testing.T) {
		facts := BuildWorkspaceFacts(nil)
		if facts == nil || len(facts.TopicKeys) != 0 || len(facts.Ports) != 0 {
			t.Fatalf("facts = %+v", facts)
		}
		if _, ok := facts.ContractFor(TopicKey{Pubsub: "p", Name: "t"}); ok {
			t.Fatal("empty index should have no contracts")
		}
	})
}
