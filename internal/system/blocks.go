package system

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// parseBlock reads one scaffold record's kind-specific values into the
// component's wiring. The parser owns per-record validation only; every
// cross-component check (topic contract conflicts, port uniqueness)
// stays in Assemble, gated on the component's shape.
type parseBlock func(e template.ScaffoldEntry, c *Component) error

// blockParsers is the set of block kinds Assemble accepts. Records with a
// kind absent from the registry are skipped with a warning naming the
// supported kinds. Adding a kind is a constant, a parser, and an entry here.
var blockParsers = map[string]parseBlock{
	template.BlockKindExtractor:     parseTopicBlock,
	template.BlockKindLoader:        parseTopicBlock,
	template.BlockKindTransactional: parseTransactional,
}

// supportedKinds lists the registry keys sorted, for the unsupported-kind
// warning.
func supportedKinds() []string {
	kinds := make([]string, 0, len(blockParsers))
	for k := range blockParsers {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// parseTopicBlock parses the wiring of a topic block (extractor, loader).
// A block-shaped record reads its message wiring from subscribe/publishes
// blocks and its transport from the resolution rules below; a legacy flat
// record keeps the topic+contract pair that always paired the halves.
func parseTopicBlock(e template.ScaffoldEntry, c *Component) error {
	if template.HasMessageBlocks(e.Values) {
		return parseMessageBlocks(e, c)
	}

	// Legacy flat-key record. Directionless: the shared topic key pairs the
	// halves, so the direction comes from the block kind — the same rule
	// the host template applies today — and the message name is the topic
	// name, the only message identity this vocabulary had.
	topic, err := template.RecordValue(e, template.KeyTopic)
	if err != nil {
		return err
	}
	contract, err := template.RecordValue(e, template.KeyContract)
	if err != nil {
		return fmt.Errorf("%w\nRe-scaffold this integration with a template release that records the contract type, or add \"contract\": \"<TypeName>\" to the record's values", err)
	}
	pubsub, err := template.RecordValueDefault(e, template.KeyPubsub, template.DefaultPubsub)
	if err != nil {
		return err
	}
	c.Topic = &TopicKey{Pubsub: pubsub, Name: topic}
	c.topicContract = contract
	c.Message = legacyMessageWiring(c.Kind, topic, contract)

	// No fallback for the port: a default would describe a binding the
	// rendered code doesn't use. A missing key is reported by the caller as
	// a warning; a present one must be a non-empty string.
	if _, ok := e.Values[template.KeyPort]; ok {
		port, err := template.RecordValue(e, template.KeyPort)
		if err != nil {
			return err
		}
		c.Port = port
		c.Ports = []string{port}
	} else {
		c.missingPort = true
	}
	return nil
}

// legacyMessageWiring derives the message view of a legacy record. Both
// halves keep the topic as the channel and its name as the message name:
// resolving a block-shaped subscriber against a legacy producer relies on
// the two name spaces agreeing.
func legacyMessageWiring(kind, topic, contract string) *MessageWiring {
	m := &MessageWiring{Name: topic, Contract: contract}
	switch kind {
	case template.BlockKindExtractor:
		m.Kind = MessagePublish
	case template.BlockKindLoader:
		m.Kind = MessageSubscribe
	}
	return m
}

// parseMessageBlocks parses the block wiring shape: at most one of
// subscribe/publishes — both in one record is a record that contradicts
// itself, never a component with two directions.
func parseMessageBlocks(e template.ScaffoldEntry, c *Component) error {
	sub, err := template.ReadSubscribeBlock(e)
	if err != nil {
		return err
	}
	pub, err := template.ReadPublishesBlock(e)
	if err != nil {
		return err
	}
	if sub != nil && pub != nil {
		return fmt.Errorf("%s carries both a subscribe and a publishes block; one component is one direction — keep the publish in its producing record and the subscribe in its consuming record", recordRef(e))
	}
	// A half-wired snapshot is a broken record in either direction: topic
	// without pubsub and pubsub without topic both name a channel the
	// system cannot assemble, and silently dropping the recorded half
	// would fabricate a different one.
	if pub != nil && pub.Pubsub != "" && pub.Topic == "" {
		_, err := blockRequiredField(e, template.KeyPublishes, template.KeyTopic, template.KeyPubsub, pub.Topic)
		return err
	}
	switch {
	case pub != nil:
		c.Message = &MessageWiring{Kind: MessagePublish, Name: pub.Message, Contract: pub.Contract, Dataschema: pub.Dataschema, External: pub.External()}
		// A registry-resolved publication carries its channel snapshot: the
		// recorded channel wins exactly as a legacy record's does, and the
		// message stays out of the internal messagegroup (the registry
		// already serves its definition). An internal publication defaults
		// to the system pubsub on a topic named after the message.
		if pub.External() {
			pubsub, err := blockRequiredField(e, template.KeyPublishes, template.KeyPubsub, template.KeyTopic, pub.Pubsub)
			if err != nil {
				return err
			}
			c.Topic = &TopicKey{Pubsub: pubsub, Name: pub.Topic}
		}
		// The topic entry carries this contract whether the channel was
		// recorded or defaulted — without it, two producers on one channel
		// could disagree on the contract and dress the disagreement up as
		// two unrelated topic entries.
		c.topicContract = pub.Contract
	case sub != nil:
		c.Message = &MessageWiring{Kind: MessageSubscribe, Name: sub.Message, Dataschema: sub.Dataschema}
		c.Message.External = sub.External()
		if sub.External() {
			pubsub, err := blockRequiredField(e, template.KeySubscribe, template.KeyPubsub, template.KeyTopic, sub.Pubsub)
			if err != nil {
				return err
			}
			c.Topic = &TopicKey{Pubsub: pubsub, Name: sub.Topic}
		}
	}

	// A port carries over from any shape: block writers emit it like the
	// flat writers did.
	if _, ok := e.Values[template.KeyPort]; ok {
		port, err := template.RecordValue(e, template.KeyPort)
		if err != nil {
			return err
		}
		c.Port = port
		c.Ports = []string{port}
	} else {
		c.missingPort = true
	}
	return nil
}

// blockRequiredField re-reports an empty required snapshot half as the
// paired-field error, naming both keys so the fix is one edit away.
func blockRequiredField(e template.ScaffoldEntry, block, key, triggerKey, value string) (string, error) {
	if value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s: values.%s.%s is required when values.%s.%s is set", recordRef(e), block, key, block, triggerKey)
}

// recordRef names the record the way parse errors across assembly do.
func recordRef(e template.ScaffoldEntry) string {
	return filepath.Join(e.Path, filepath.FromSlash(template.ScaffoldRelPath))
}

// parseTransactional parses the wiring of a transactional block: exactly
// two ports, From before To, and no topic. Both ports are required — a
// half-wired transactional block would render broken code.
func parseTransactional(e template.ScaffoldEntry, c *Component) error {
	from, err := template.RecordValue(e, template.KeyFromPort)
	if err != nil {
		return err
	}
	to, err := template.RecordValue(e, template.KeyToPort)
	if err != nil {
		return err
	}
	c.Ports = []string{from, to}
	return nil
}
