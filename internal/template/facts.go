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
// the external ports already named, the internal messages declared by
// publishes blocks, plus the one organization the records agree on. Create
// flows derive parameter suggestions from it; it is built by callers that
// scan workspaces (internal/system) and consumed read-only by value
// resolution.
//
// Two fields are ambient inputs a caller seeds for the run rather than
// facts a scan derived: messageCandidates (registry refs offered next to
// the internal messages) and messageParams (the template's message
// parameters, identified from the fetched manifest). Both live here so
// Suggest stays a single flat registry keyed by parameter name.
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

	// messages maps an internal message name (from publishes blocks) to its
	// declared contract, when one was recorded.
	messages map[string]string

	// messageCandidates are registry message refs offered as prompt
	// suggestions for message parameters, seeded by the caller.
	messageCandidates []string

	// messageParams names the template parameters that carry message
	// wiring, identified from the fetched manifest's label. Parameters
	// outside this set get no message suggestions whatever their name.
	messageParams map[string]bool
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

// AddMessageCandidates seeds the registry message candidates offered as
// prompt suggestions for message parameters. Internal message names come
// from the workspace's publishes blocks; these join them, and callers
// normally pass every ref the registry serves.
func (f *WorkspaceFacts) AddMessageCandidates(refs []string) {
	if f == nil {
		return
	}
	f.messageCandidates = append(f.messageCandidates, refs...)
}

// MessageCandidates returns the deduplicated suggestion pool for message
// parameters: internal messages declared by the workspace's publishes
// blocks plus the seeded registry refs, merged and sorted.
func (f *WorkspaceFacts) MessageCandidates() []string {
	if f == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for name := range f.messages {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, ref := range f.messageCandidates {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

// SetMessageParameters names the template's message parameters. It is
// derived from the fetched manifest, so the prompt-time suggestion only
// fires for templates that declare message wiring — a template whose
// parameters merely share a name keeps working as before.
func (f *WorkspaceFacts) SetMessageParameters(names []string) {
	if f == nil {
		return
	}
	if f.messageParams == nil {
		f.messageParams = map[string]bool{}
	}
	for _, n := range names {
		f.messageParams[n] = true
	}
}

// IsMessageParameter reports whether the template's manifest identifies
// name as carrying message wiring.
func (f *WorkspaceFacts) IsMessageParameter(name string) bool {
	return f != nil && f.messageParams[name]
}

// ContractForMessage returns the contract a workspace record declared for
// an internal message name, or ("", false) when nothing declared it.
func (f *WorkspaceFacts) ContractForMessage(name string) (string, bool) {
	if f == nil {
		return "", false
	}
	c, ok := f.messages[name]
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
	facts := &WorkspaceFacts{contracts: map[TopicKey]string{}, messages: map[string]string{}}
	type contractSighting struct {
		contract   string
		conflicted bool
	}
	sightings := map[TopicKey]*contractSighting{}
	seenTopic := map[TopicKey]bool{}
	seenPort := map[string]bool{}
	orgSeen := false
	orgConflicted := false

	indexTopic := func(key TopicKey, contract string) {
		if !seenTopic[key] {
			seenTopic[key] = true
			facts.TopicKeys = append(facts.TopicKeys, key)
		}
		if contract != "" {
			if s, seen := sightings[key]; seen {
				if s.contract != contract {
					s.conflicted = true
				}
			} else {
				sightings[key] = &contractSighting{contract: contract}
			}
		}
	}

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
			// Block-shaped records ignore the legacy flat keys: the block is
			// the wiring when present, and reading both would invent a merge
			// the record never declared. Errors from misshaped blocks stay
			// out of suggestions — a suggestion aid never fails; assembly is
			// the surface that reports them.
			if HasMessageBlocks(e.Values) {
				if sub, err := ReadSubscribeBlock(entry(e)); err == nil && sub != nil {
					if sub.External() {
						indexTopic(TopicKey{Pubsub: sub.Pubsub, Name: sub.Topic}, "")
					}
				}
				if pub, err := ReadPublishesBlock(entry(e)); err == nil && pub != nil {
					if _, seen := facts.messages[pub.Message]; !seen {
						facts.messages[pub.Message] = pub.Contract
					}
				}
				if port, ok := SoftValue(e.Values, KeyPort); ok && !seenPort[port] {
					seenPort[port] = true
					facts.Ports = append(facts.Ports, port)
				}
				break
			}

			// Legacy flat-key record: topic and contract pair the halves.
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
				indexTopic(TopicKey{Pubsub: pubsub, Name: topic}, contract)
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

// entry adapts the fact entry into the ScaffoldEntry shape the strict
// block readers consume: same values, zero path beyond the record name.
func entry(e WorkspaceFactEntry) ScaffoldEntry {
	return ScaffoldEntry{Scaffold: Scaffold{Values: e.Values}}
}
