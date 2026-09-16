package template

import "testing"

func factEntry(kind string, values map[string]any) WorkspaceFactEntry {
	return WorkspaceFactEntry{BlockKind: kind, Values: values}
}

func TestBuildWorkspaceFactsMessageFirst(t *testing.T) {
	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		factEntry(BlockKindExtractor, map[string]any{
			KeyAppID: "order-extractor", KeyPublishes: "order-created", KeyPort: "order-source", KeyOrganization: "Acme",
		}),
		factEntry(BlockKindLoader, map[string]any{
			KeyAppID: "order-loader", KeySubscribes: "order-created", KeyPort: "order-destination", KeyOrganization: "Acme",
		}),
		factEntry(BlockKindTransactional, map[string]any{
			KeyFromPort: "erp-source", KeyToPort: "erp-destination",
		}),
	})

	if got := facts.MessageCandidates(); len(got) != 1 || got[0] != "order-created" {
		t.Fatalf("MessageCandidates = %v", got)
	}
	if c, ok := facts.ContractForMessage("order-created"); !ok || c != "OrderCreated" {
		t.Fatalf("ContractForMessage = %q, %v", c, ok)
	}
	if len(facts.TopicKeys) != 1 || facts.TopicKeys[0] != (TopicKey{Pubsub: DefaultPubsub, Name: "order-created"}) {
		t.Fatalf("TopicKeys = %+v", facts.TopicKeys)
	}
	if got, ok := facts.Organization(); !ok || got != "Acme" {
		t.Fatalf("Organization = %q, %v", got, ok)
	}
	if len(facts.Ports) != 4 {
		t.Fatalf("Ports = %v", facts.Ports)
	}
}

func TestBuildWorkspaceFactsConflictingOrganization(t *testing.T) {
	facts := BuildWorkspaceFacts([]WorkspaceFactEntry{
		factEntry(BlockKindExtractor, map[string]any{KeyOrganization: "Acme", KeyPublishes: "a"}),
		factEntry(BlockKindLoader, map[string]any{KeyOrganization: "Other", KeySubscribes: "a"}),
	})
	if got, ok := facts.Organization(); ok || got != "" {
		t.Fatalf("Organization = %q, %v; want empty on conflict", got, ok)
	}
}
