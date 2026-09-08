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

	t.Run("agreeing records contribute one organization, whatever the block kind", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{"topic": "orders", "contract": "Order", "organization": "acme"}),
			factEntry(BlockKindLoader, map[string]any{"topic": "orders", "contract": "Order", "organization": "acme"}),
			factEntry("system-host", map[string]any{"organization": "acme"}),
		}
		facts := BuildWorkspaceFacts(entries)
		if got, ok := facts.Organization(); !ok || got != "acme" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
	})

	t.Run("disagreeing records demote the organization", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{"organization": "acme"}),
			factEntry(BlockKindLoader, map[string]any{"organization": "globex"}),
		}
		facts := BuildWorkspaceFacts(entries)
		if got, ok := facts.Organization(); ok || got != "" {
			t.Fatalf("conflicted organization should demote, got %q, %v", got, ok)
		}
	})

	t.Run("SetOrganization fills an unset fact and never overrides one", func(t *testing.T) {
		facts := BuildWorkspaceFacts(nil)
		facts.SetOrganization("acme")
		facts.SetOrganization("globex")
		if got, ok := facts.Organization(); !ok || got != "acme" {
			t.Fatalf("organization = %q, %v", got, ok)
		}
		workspace := BuildWorkspaceFacts([]WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{"organization": "globex"}),
		})
		workspace.SetOrganization("acme")
		if got, ok := workspace.Organization(); !ok || got != "globex" {
			t.Fatalf("workspace organization = %q, %v", got, ok)
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
		if got, ok := facts.Organization(); ok || got != "" {
			t.Fatalf("empty index should have no organization, got %q, %v", got, ok)
		}
	})
}

func TestBuildWorkspaceFactsMessageBlocks(t *testing.T) {
	t.Run("publishes blocks index internal messages with their contracts", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "product-sink", "publishes": map[string]any{"message": "product-exported", "contract": "ProductExported"},
			}),
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "order-sink", "publishes": map[string]any{"message": "order-exported"},
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if got := facts.MessageCandidates(); len(got) != 2 || got[0] != "order-exported" || got[1] != "product-exported" {
			t.Errorf("MessageCandidates() = %v, want both internal messages sorted", got)
		}
		if c, ok := facts.ContractForMessage("product-exported"); !ok || c != "ProductExported" {
			t.Errorf("ContractForMessage = %q, %v", c, ok)
		}
	})

	t.Run("external subscribe snapshots join the topic facts", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindLoader, map[string]any{
				"appId": "loader", "subscribe": map[string]any{
					"message": "io.intropy.maxbo.product.export",
					"pubsub":  "product-distribution-pubsub",
					"topic":   "sbt-test-product-extractor-001",
				},
			}),
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "legacy", "topic": "orders", "contract": "Order",
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 2 {
			t.Fatalf("topic keys = %+v, want the legacy key and the snapshot key", facts.TopicKeys)
		}
		if facts.TopicKeys[1].Name != "orders" || facts.TopicKeys[1].Pubsub != "pubsub" {
			t.Errorf("legacy key = %+v, want (pubsub, orders)", facts.TopicKeys[1])
		}
	})

	t.Run("internal subscribe without snapshot adds no topic", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindLoader, map[string]any{
				"appId": "loader", "subscribe": map[string]any{"message": "product-exported"},
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.TopicKeys) != 0 {
			t.Errorf("topic keys = %+v, want none — the channel is assembly's answer", facts.TopicKeys)
		}
		if got := facts.MessageCandidates(); len(got) != 0 {
			t.Errorf("candidates = %v, want none — a subscribe block declares no message", got)
		}
	})

	t.Run("mixed old and new records union without duplicates", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "legacy", "topic": "product-exported", "contract": "ProductExported",
			}),
			factEntry(BlockKindExtractor, map[string]any{
				"appId": "new", "publishes": map[string]any{"message": "product-exported", "contract": "ProductExported"},
			}),
		}
		facts := BuildWorkspaceFacts(entries)
		got := facts.MessageCandidates()
		if len(got) != 1 || got[0] != "product-exported" {
			t.Errorf("MessageCandidates() = %v, want the message once", got)
		}
	})

	t.Run("message parameter registry gates suggestions", func(t *testing.T) {
		facts := BuildWorkspaceFacts(nil)
		if facts.IsMessageParameter("message") {
			t.Error("no parameters are message parameters before the manifest names them")
		}
		facts.SetMessageParameters([]string{"message"})
		facts.AddMessageCandidates([]string{"io.intropy.maxbo.product.export"})
		if !facts.IsMessageParameter("message") {
			t.Error("the named parameter should be a message parameter")
		}
		if got := facts.MessageCandidates(); len(got) != 1 || got[0] != "io.intropy.maxbo.product.export" {
			t.Errorf("candidates = %v", got)
		}
	})

	t.Run("malformed blocks are skipped, not fatal", func(t *testing.T) {
		entries := []WorkspaceFactEntry{
			factEntry(BlockKindExtractor, map[string]any{"publishes": "not-a-map"}),
			factEntry(BlockKindExtractor, map[string]any{"subscribe": map[string]any{}}),
		}
		facts := BuildWorkspaceFacts(entries)
		if len(facts.MessageCandidates()) != 0 {
			t.Errorf("candidates = %v, want none", facts.MessageCandidates())
		}
	})
}
