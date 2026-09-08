package template

import (
	"fmt"
	"path/filepath"
)

// The wiring vocabulary: the parameter names block templates record in
// their scaffold values and later commands (sys create/update, deploy,
// prompt suggestions) read back. They are CLI-owned convention, not
// manifest schema — a template that names its parameters otherwise simply
// gets no assembly or suggestions. One home so a vocabulary change has
// one diff, not a scavenger hunt.
const (
	KeyAppID        = "appId"
	KeyTopic        = "topic"
	KeyContract     = "contract"
	KeyPubsub       = "pubsub"
	KeyPort         = "port"
	KeyFromPort     = "fromPort"
	KeyToPort       = "toPort"
	KeyName         = "name"
	KeyOrganization = "organization"
	KeyProjectName  = "projectName"
	KeySystemClass  = "systemClass"

	// The message wiring blocks. The flat topic/contract/pubsub keys above
	// are superseded by these: a scaffold carries either a subscribe block
	// (what the component consumes, from the registry or a producing
	// sibling) or a publishes block (what it declares). Both shapes are
	// read everywhere; writers emit only the blocks.
	KeySubscribe     = "subscribe"
	KeyPublishes     = "publishes"
	KeyMessage       = "message"
	KeyDataschema    = "dataschema"
	KeyDataschemaURL = "dataschemaurl"

	// DefaultPubsub is the pub/sub component a record belongs to when it
	// predates the pubsub value being recorded.
	DefaultPubsub = "pubsub"
)

// RecordValue reads key from a scaffold record's values as a non-empty
// string. Missing, mistyped, and empty values are errors naming the
// record, so the user knows which project to fix. It is the strict
// regime: callers validating a workspace (system assembly) use it;
// callers offering suggestions use SoftValue instead.
func RecordValue(e ScaffoldEntry, key string) (string, error) {
	record := filepath.Join(e.Path, filepath.FromSlash(ScaffoldRelPath))
	v, ok := e.Values[key]
	if !ok {
		return "", fmt.Errorf("%s: values.%s is missing", record, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: values.%s has type %T, expected string", record, key, v)
	}
	if s == "" {
		return "", fmt.Errorf("%s: values.%s is empty", record, key)
	}
	return s, nil
}

// RecordValueDefault is RecordValue with a fallback for records that
// predate the value being recorded. Only a missing key falls back; a
// present but mistyped or empty value is still an error — a record that
// names the key must say something meaningful.
func RecordValueDefault(e ScaffoldEntry, key, fallback string) (string, error) {
	if _, ok := e.Values[key]; !ok {
		return fallback, nil
	}
	return RecordValue(e, key)
}

// SoftValue reads key from a values map as a non-empty string, reporting
// false for missing, mistyped, or empty values. It is the lenient regime:
// suggestion aids and best-effort joins skip such records rather than
// failing an operation the records themselves would still allow.
func SoftValue(values map[string]any, key string) (string, bool) {
	s, ok := values[key].(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// The message wiring blocks. A subscribe block names the message the
// component consumes; without pubsub/topic it is an internal subscription
// whose channel assembly resolves from the producing component. With the
// snapshot fields it is an external subscription — the registry is never
// contacted again for it.
//
// A publishes block declares what its component produces; internal and
// registry-resolved publications differ only in which fields they carry,
// and PublishesBlock's doc comment owns that split.
type SubscribeBlock struct {
	Message       string
	Pubsub        string
	Topic         string
	Dataschema    string
	DataschemaURL string
}

// PublishesBlock is a producer's message declaration. A registry-resolved
// publish carries the full channel snapshot (pubsub, topic) and the schema
// pin; a producer-declared internal publish carries only the message.
// Contract is the .NET shared-project type name, carried during the
// transition while host templates render Topics.cs from it; it is not the
// CloudEvents type.
// Contract is the .NET shared-project type name, carried during the
// transition while host templates render Topics.cs from it; it is not the
// CloudEvents type.
type PublishesBlock struct {
	Message       string
	Contract      string
	Dataschema    string
	DataschemaURL string
	Pubsub        string
	Topic         string
}

// External reports whether the declaration carries its own channel
// snapshot. External publications assemble their channel from the record
// alone; internal publications default to the system pubsub on a topic
// named after the message.
func (b *PublishesBlock) External() bool { return b.Topic != "" }

// External reports whether the subscription carries its own channel
// snapshot. External subscriptions assemble without a producer and without
// touching the registry.
func (b *SubscribeBlock) External() bool { return b.Topic != "" }

// HasSubscribeValue reports whether values carry a subscribe block. Block
// presence wins over the legacy flat keys wherever both appear — the dual
// read is a fallback, never a merge.
func HasSubscribeValue(values map[string]any) bool {
	_, ok := values[KeySubscribe]
	return ok
}

// HasPublishesValue reports whether values carry a publishes block.
func HasPublishesValue(values map[string]any) bool {
	_, ok := values[KeyPublishes]
	return ok
}

// HasMessageBlocks reports whether values use the block wiring shape at
// all. Assembly and facts treat such records as block-shaped and ignore
// their legacy flat keys.
func HasMessageBlocks(values map[string]any) bool {
	return HasSubscribeValue(values) || HasPublishesValue(values)
}

// blockMap reads one block value as the map the writers produce. The
// strict regime applies: a present block must be an object, so a mistyped
// block is an error naming the record and the block, never a silent skip.
func blockMap(e ScaffoldEntry, block string) (map[string]any, error) {
	v, ok := e.Values[block]
	if !ok {
		return nil, nil // absent is not an error; the caller decides
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: values.%s has type %T, expected object", recordPath(e), block, v)
	}
	return m, nil
}

// blockString reads a strict non-empty string field out of a block: mistyped
// or empty names the record and the full key path.
func blockString(e ScaffoldEntry, block, key string) (string, error) {
	m, err := blockMap(e, block)
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: values.%s.%s has type %T, expected string", recordPath(e), block, key, v)
	}
	if s == "" {
		return "", fmt.Errorf("%s: values.%s.%s is empty", recordPath(e), block, key)
	}
	return s, nil
}

// recordPath names the record in error messages, matching RecordValue.
func recordPath(e ScaffoldEntry) string {
	return filepath.Join(e.Path, filepath.FromSlash(ScaffoldRelPath))
}

// ReadSubscribeBlock strictly reads the subscribe block. The message name
// is required; the snapshot fields are each required to be non-empty
// strings when present. A record with the block and empty message is a
// broken record, not a wireless one.
func ReadSubscribeBlock(e ScaffoldEntry) (*SubscribeBlock, error) {
	if !HasSubscribeValue(e.Values) {
		return nil, nil
	}
	message, err := blockString(e, KeySubscribe, KeyMessage)
	if err != nil {
		return nil, err
	}
	if message == "" {
		return nil, fmt.Errorf("%s: values.%s.%s is missing", recordPath(e), KeySubscribe, KeyMessage)
	}
	b := &SubscribeBlock{Message: message}
	for _, f := range []struct {
		key string
		dst *string
	}{
		{KeyPubsub, &b.Pubsub},
		{KeyTopic, &b.Topic},
		{KeyDataschema, &b.Dataschema},
		{KeyDataschemaURL, &b.DataschemaURL},
	} {
		s, err := blockString(e, KeySubscribe, f.key)
		if err != nil {
			return nil, err
		}
		*f.dst = s
	}
	return b, nil
}

// ReadPublishesBlock strictly reads the publishes block. The message name
// is required; contract, dataschema, and the channel snapshot stay optional
// for internal declarations. The pubsub/topic pair is validated by system
// assembly, not here — the block reader stays a shape reader, matching how
// the subscribe branch splits concerns.
func ReadPublishesBlock(e ScaffoldEntry) (*PublishesBlock, error) {
	if !HasPublishesValue(e.Values) {
		return nil, nil
	}
	message, err := blockString(e, KeyPublishes, KeyMessage)
	if err != nil {
		return nil, err
	}
	if message == "" {
		return nil, fmt.Errorf("%s: values.%s.%s is missing", recordPath(e), KeyPublishes, KeyMessage)
	}
	b := &PublishesBlock{Message: message}
	for _, f := range []struct {
		key string
		dst *string
	}{
		{KeyContract, &b.Contract},
		{KeyDataschema, &b.Dataschema},
		{KeyDataschemaURL, &b.DataschemaURL},
		{KeyPubsub, &b.Pubsub},
		{KeyTopic, &b.Topic},
	} {
		s, err := blockString(e, KeyPublishes, f.key)
		if err != nil {
			return nil, err
		}
		*f.dst = s
	}
	return b, nil
}

// SubscribeBlockValue renders the block as the values-map entry writers
// emit. Writers never add the legacy flat keys alongside it.
func SubscribeBlockValue(b *SubscribeBlock) map[string]any {
	m := map[string]any{KeyMessage: b.Message}
	if b.Pubsub != "" {
		m[KeyPubsub] = b.Pubsub
	}
	if b.Topic != "" {
		m[KeyTopic] = b.Topic
	}
	if b.Dataschema != "" {
		m[KeyDataschema] = b.Dataschema
	}
	if b.DataschemaURL != "" {
		m[KeyDataschemaURL] = b.DataschemaURL
	}
	return m
}

// PublishesBlockValue renders a publishes declaration as the values-map
// entry writers emit.
func PublishesBlockValue(b *PublishesBlock) map[string]any {
	m := map[string]any{KeyMessage: b.Message}
	for _, f := range []struct {
		key   string
		value string
	}{
		{KeyContract, b.Contract},
		{KeyDataschema, b.Dataschema},
		{KeyDataschemaURL, b.DataschemaURL},
		{KeyPubsub, b.Pubsub},
		{KeyTopic, b.Topic},
	} {
		if f.value != "" {
			m[f.key] = f.value
		}
	}
	return m
}
