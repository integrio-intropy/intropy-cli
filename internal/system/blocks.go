package system

import (
	"fmt"
	"sort"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// parseBlock reads one scaffold record's kind-specific values into the
// component's wiring. The parser owns per-record validation only; every
// cross-component check (topic contract conflicts, connector uniqueness)
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

// parseTopicBlock parses the wiring of a topic block (extractor, loader):
// a required topic and contract, and one optional connector. A record
// without a connector stays a valid component — it just gets no From/To in
// the generated wiring.
func parseTopicBlock(e template.ScaffoldEntry, c *Component) error {
	topic, err := stringValue(e, "topic")
	if err != nil {
		return err
	}
	contract, err := stringValue(e, "contract")
	if err != nil {
		return fmt.Errorf("%w\nRe-scaffold this integration with a template release that records the contract type, or add \"contract\": \"<TypeName>\" to the record's values.", err)
	}
	pubsub, err := stringValueDefault(e, "pubsub", "pubsub")
	if err != nil {
		return err
	}
	c.Topic = &TopicKey{Pubsub: pubsub, Name: topic}
	c.topicContract = contract

	// No fallback for the connector: a default would describe a binding
	// the rendered code doesn't use. A missing key is reported by the
	// caller as a warning; a present one must be a non-empty string.
	if _, ok := e.Values["connector"]; ok {
		connector, err := stringValue(e, "connector")
		if err != nil {
			return err
		}
		c.Connector = connector
		c.Connectors = []string{connector}
	} else {
		c.missingConnector = true
	}
	return nil
}

// parseTransactional parses the wiring of a transactional block: exactly
// two connectors, From before To, and no topic. Both connectors are
// required — a half-wired transactional block would render broken code.
func parseTransactional(e template.ScaffoldEntry, c *Component) error {
	from, err := stringValue(e, "fromConnector")
	if err != nil {
		return err
	}
	to, err := stringValue(e, "toConnector")
	if err != nil {
		return err
	}
	c.Connectors = []string{from, to}
	return nil
}
