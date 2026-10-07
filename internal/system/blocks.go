package system

import (
	"fmt"
	"sort"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// parseBlock reads one scaffold record's kind-specific values into the
// component's wiring. The parser owns per-record validation only; every
// cross-component check stays in Assemble, gated on the component's shape.
type parseBlock func(e template.ScaffoldEntry, c *Component) error

// blockParsers is the set of block kinds Assemble accepts. Records with a
// kind absent from the registry are skipped with a warning naming the
// supported kinds. Adding a kind is a constant, a parser, and an entry here.
var blockParsers = map[string]parseBlock{
	template.BlockKindExtractor:     parseExtractor,
	template.BlockKindLoader:        parseLoader,
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

// parseExtractor reads the one message an extractor publishes. The message
// identity is the declaration; system assembly derives the channel and
// payload type from it.
func parseExtractor(e template.ScaffoldEntry, c *Component) error {
	message, err := template.ReadPublishesMessage(e)
	if err != nil {
		return preScalarMessageRecord(err)
	}
	contract := template.PascalCase(message)
	if contract == "" {
		return nil
	}
	channel, err := template.ReadChannel(e)
	if err != nil {
		return err
	}
	if channel == "" {
		channel = message
	}
	c.channel = channel
	c.Topic = &TopicKey{Pubsub: template.DefaultPubsub, Name: channel}
	c.topicContract = contract
	c.Message = &MessageWiring{Kind: MessagePublish, Name: message, Contract: contract}
	return parseSinglePort(e, c)
}

// parseLoader reads the messages a loader routes — its routes, or the one
// message a record without routes subscribes to — what it does with the rest
// of its topic, and the topic it declares. Assembly checks every routed
// message's producer publishes on that topic and fails if one does not.
func parseLoader(e template.ScaffoldEntry, c *Component) error {
	routes, err := template.ReadRoutes(e)
	if err != nil {
		if _, routed := e.Values[template.KeyRoutes]; routed {
			return err
		}
		return preScalarMessageRecord(err)
	}
	if c.Default, err = template.ReadDefault(e); err != nil {
		return err
	}
	if c.channel, err = template.ReadChannel(e); err != nil {
		return err
	}
	c.Routes = routes
	c.Message = &MessageWiring{Kind: MessageSubscribe, Name: routes[0].Message}
	return parseSinglePort(e, c)
}

// preScalarMessageRecord reports a message-wiring read failure as the
// migration it is: a record scaffolded before the scalar message DSL carries
// nested blocks or legacy flat keys, and no fallback reads them. The guidance
// names both migration paths — set the scalar key, or re-scaffold.
func preScalarMessageRecord(err error) error {
	return fmt.Errorf("%w\nthis record predates the scalar message DSL: set values.publishes (extractor) or values.subscribes (loader) to the message name in the record's values, or re-scaffold the integration with a current template release", err)
}

func parseSinglePort(e template.ScaffoldEntry, c *Component) error {
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

// parseTransactional parses the wiring of a transactional block: exactly
// two ports, From before To, and no message. Both ports are required — a
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
