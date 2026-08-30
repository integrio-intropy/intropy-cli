package template

// Suggest computes prompt-time candidate values for a template's declared
// fields from workspace facts and the values confirmed so far this run.
// The result is keyed by parameter name; fields the convention registry
// does not know, and fields with no candidates, are absent.
//
// The registry is CLI-owned convention, not manifest schema: it mirrors
// the value names the block templates record (topic, pubsub, contract —
// the same names internal/system's block parsers assemble from). A
// template whose parameters use other names simply gets no suggestions.
//
// Ports are deliberately absent: every block needs its own port (system
// assembly rejects a shared one), so an already-taken port name is an
// anti-suggestion — offering it would steer scaffolds toward a collision.
//
// Suggestions are prompt metadata only. They never enter the values map on
// their own: a candidate becomes a value only when the prompter confirms
// it with the user, and callers record the confirmed value under the
// field's name — never the candidate list.
func Suggest(fields []FieldSpec, facts *WorkspaceFacts, confirmed map[string]any) map[string][]string {
	if facts == nil {
		return nil
	}
	out := map[string][]string{}
	for _, f := range fields {
		candidates := suggestField(f, facts, confirmed)
		candidates = dropDefaultCandidate(candidates, f)
		if len(candidates) > 0 {
			out[f.Name] = candidates
		}
	}
	return out
}

// suggestField is the convention registry: parameter name -> candidates
// derivable from the facts and the values confirmed so far. Chained
// derivation (contract follows the confirmed topic) re-reads the confirmed
// values on every call rather than threading field-to-field wiring, so the
// registry stays a flat name-to-rule table.
func suggestField(f FieldSpec, facts *WorkspaceFacts, confirmed map[string]any) []string {
	switch f.Name {
	case "topic":
		topics := make([]string, 0, len(facts.TopicKeys))
		for _, key := range facts.TopicKeys {
			topics = append(topics, key.Name)
		}
		return topics
	case "pubsub":
		seen := map[string]bool{}
		var pubs []string
		for _, key := range facts.TopicKeys {
			if !seen[key.Pubsub] {
				seen[key.Pubsub] = true
				pubs = append(pubs, key.Pubsub)
			}
		}
		return pubs
	case "contract":
		// With a confirmed topic, the contract is constrained to it; a
		// conflicted or unknown key yields nothing rather than a guess.
		// Without one, every known contract is a candidate.
		if topic, ok := confirmedString(confirmed, "topic"); ok {
			pubsub, _ := confirmedString(confirmed, "pubsub")
			if pubsub == "" {
				pubsub = "pubsub"
			}
			if contract, ok := facts.ContractFor(TopicKey{Pubsub: pubsub, Name: topic}); ok {
				return []string{contract}
			}
			return nil
		}
		return knownContracts(facts)
	}
	return nil
}

// knownContracts lists every unconflicted contract the facts carry, in
// topic-key order with duplicates removed.
func knownContracts(facts *WorkspaceFacts) []string {
	seen := map[string]bool{}
	var out []string
	for _, key := range facts.TopicKeys {
		contract, ok := facts.ContractFor(key)
		if ok && !seen[contract] {
			seen[contract] = true
			out = append(out, contract)
		}
	}
	return out
}

// dropDefaultCandidate removes candidates the field's schema default
// already communicates — the prompt's default bracket and a suggestion
// list must never compete for the same value.
func dropDefaultCandidate(candidates []string, f FieldSpec) []string {
	def, ok := f.Default.(string)
	if !ok || def == "" {
		return candidates
	}
	out := candidates[:0]
	for _, c := range candidates {
		if c != def {
			out = append(out, c)
		}
	}
	return out
}

// confirmedString reads a confirmed value as a non-empty string. Confirmed
// values arrive from --set/--values/prompts and are already schema-coerced
// for declared fields; unknown or mistyped entries are treated as absent.
func confirmedString(confirmed map[string]any, key string) (string, bool) {
	s, ok := confirmed[key].(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}
