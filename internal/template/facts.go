package template

import (
	"sort"
)

// TopicKey identifies a pub/sub topic within a workspace: the pub/sub
// component it lives on plus the topic name. It is the key workspace facts
// dedupe on — two scaffold records naming the same key declare the two
// halves of one contract.
//
// It is the canonical "which topic" shape for the whole CLI —
// internal/system aliases it for assembly — and its JSON tags are the
// wire vocabulary `sys create --output json` already serves.
type TopicKey struct {
	Pubsub string `json:"pubsub"`
	Name   string `json:"topic"`
}

// WorkspaceFacts is the prompt-time view of what a workspace's scaffold
// records already declare: the topics in use, the contract each carries,
// and the external ports already named, plus the one organization the
// records agree on. Create flows derive parameter
// suggestions from it; it is built by callers that scan workspaces
// (internal/system) and consumed read-only by value resolution.
//
// BuildWorkspaceFacts constructs one from scaffold entries; the zero value
// is a valid empty index.
type WorkspaceFacts struct {
	// TopicKeys holds the distinct topic keys in use, sorted by
	// (Pubsub, Topic).
	TopicKeys []TopicKey

	// Ports holds the distinct external port names in use, sorted
	// lexicographically.
	Ports []string

	// organization is the single organization the workspace's block records
	// declare. Records naming different organizations demote the fact — a
	// component belongs to exactly one organization, so a workspace that
	// disagrees with itself has nothing to suggest.
	organization string

	// contracts maps a topic key to its recorded contract type. A key whose
	// records disagree on the contract is absent — a conflicted fact is no
	// fact, and callers treat absence as "no suggestion".
	contracts map[TopicKey]string
}

// Organization returns the organization the workspace's block records
// agree on, or ("", false) when no record names one or they disagree.
func (f *WorkspaceFacts) Organization() (string, bool) {
	if f == nil || f.organization == "" {
		return "", false
	}
	return f.organization, true
}

// SetOrganization seeds the organization fact when the workspace has none.
// It is the caller's channel for context the records cannot carry — the
// resolved config's customer — and it never overrides a workspace-derived
// value: specific beats ambient. Call it before the facts feed Suggest.
func (f *WorkspaceFacts) SetOrganization(org string) {
	if f != nil && f.organization == "" {
		f.organization = org
	}
}

// ContractFor returns the contract type recorded for a topic key, or
// ("", false) when the key is unknown or its records conflict.
func (f *WorkspaceFacts) ContractFor(key TopicKey) (string, bool) {
	if f == nil {
		return "", false
	}
	c, ok := f.contracts[key]
	return c, ok
}

// WorkspaceFactEntry is the slice of a scaffold record fact-building reads:
// which block the record scaffolds and the values it recorded. Keeping the
// input this narrow lets BuildWorkspaceFacts stay in this package — a
// caller-side adapter maps full scaffold records to entries, and the import
// graph never cycles back through the scanner.
type WorkspaceFactEntry struct {
	// BlockKind is the record's block kind ("extractor", "loader",
	// "transactional-integration"); entries with any other kind contribute
	// nothing.
	BlockKind string

	// Values is the record's resolved parameter values.
	Values map[string]any
}

// BuildWorkspaceFacts indexes entries into workspace facts. It is
// deliberately lenient where Assemble is strict: entries with missing or
// mistyped wiring values are skipped rather than reported, because a
// suggestion aid must never fail an operation the records themselves would
// still allow. Organization is indexed from every entry with a block kind
// — a component's organization is not wiring — under the same conflict
// rule as contracts: one value wins, several demote the fact. The one
// cross-record rule it shares with Assemble is that conflicting contracts
// on one topic key demote the contract — suggesting either side of a
// conflict would bake a guess into the new record.
func BuildWorkspaceFacts(entries []WorkspaceFactEntry) *WorkspaceFacts {
	facts := &WorkspaceFacts{contracts: map[TopicKey]string{}}
	type contractSighting struct {
		contract   string
		conflicted bool
	}
	sightings := map[TopicKey]*contractSighting{}
	seenTopic := map[TopicKey]bool{}
	seenPort := map[string]bool{}
	orgSeen := false
	orgConflicted := false

	for _, e := range entries {
		if e.BlockKind == "" {
			continue
		}
		if org, ok := SoftValue(e.Values, KeyOrganization); ok {
			switch {
			case !orgSeen:
				orgSeen = true
				facts.organization = org
			case facts.organization != org:
				orgConflicted = true
			}
		}
		switch e.BlockKind {
		case BlockKindExtractor, BlockKindLoader:
			topic, tok := SoftValue(e.Values, KeyTopic)
			contract, cok := SoftValue(e.Values, KeyContract)
			// Default on the zero result, not on key absence: a present but
			// mistyped pubsub degrades to the default, the same regime as
			// before the accessors consolidated.
			pubsub, _ := SoftValue(e.Values, KeyPubsub)
			if pubsub == "" {
				pubsub = DefaultPubsub
			}
			if tok && cok {
				key := TopicKey{Pubsub: pubsub, Name: topic}
				if !seenTopic[key] {
					seenTopic[key] = true
					facts.TopicKeys = append(facts.TopicKeys, key)
				}
				if s, seen := sightings[key]; seen {
					if s.contract != contract {
						s.conflicted = true
					}
				} else {
					sightings[key] = &contractSighting{contract: contract}
				}
			}
			if port, ok := SoftValue(e.Values, KeyPort); ok && !seenPort[port] {
				seenPort[port] = true
				facts.Ports = append(facts.Ports, port)
			}
		case BlockKindTransactional:
			for _, key := range []string{KeyFromPort, KeyToPort} {
				if port, ok := SoftValue(e.Values, key); ok && !seenPort[port] {
					seenPort[port] = true
					facts.Ports = append(facts.Ports, port)
				}
			}
		}
	}

	if orgConflicted {
		facts.organization = ""
	}
	for key, s := range sightings {
		if !s.conflicted {
			facts.contracts[key] = s.contract
		}
	}
	sort.Slice(facts.TopicKeys, func(i, j int) bool {
		if facts.TopicKeys[i].Pubsub != facts.TopicKeys[j].Pubsub {
			return facts.TopicKeys[i].Pubsub < facts.TopicKeys[j].Pubsub
		}
		return facts.TopicKeys[i].Name < facts.TopicKeys[j].Name
	})
	sort.Strings(facts.Ports)
	return facts
}

