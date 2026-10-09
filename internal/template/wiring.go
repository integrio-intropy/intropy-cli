package template

import (
	"fmt"
	"path/filepath"
)

// The wiring vocabulary: the parameter names templates record in scaffold
// values and later commands read back. One home keeps DSL changes local.
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

	// The scalar message wiring keys. A producing block records publishes; a
	// consuming block records subscribes. The value is the message identity.
	KeyPublishes  = "publishes"
	KeySubscribes = "subscribes"

	// The routed subscription keys. A consuming block that routes several
	// messages records routes — a list of {message, when?}, in the order the
	// sidecar evaluates them — instead of subscribes, and default for what
	// becomes of the events no route matches. Channel overrides the topic a
	// block publishes or subscribes on, which otherwise is the message name;
	// messages sharing one topic is what lets one loader route them.
	KeyRoutes  = "routes"
	KeyMessage = "message"
	KeyWhen    = "when"
	KeyDefault = "default"
	KeyChannel = "channel"

	// The values of KeyDefault.
	DefaultDeadLetter = "dead-letter"
	DefaultIgnore     = "ignore"

	// DefaultPubsub is the pub/sub component used for every internal message.
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

// ReadPublishesMessage strictly reads the scalar publishes value from a
// producing scaffold record. The value is the message identity; the channel
// and payload type derive from it during system assembly.
func ReadPublishesMessage(e ScaffoldEntry) (string, error) {
	return RecordValue(e, KeyPublishes)
}

// ReadSubscribesMessage strictly reads the scalar subscribes value from a
// consuming scaffold record. The value must name a message published by a
// sibling component in the same workspace.
func ReadSubscribesMessage(e ScaffoldEntry) (string, error) {
	return RecordValue(e, KeySubscribes)
}

// Route is one rule of a routed subscription: the message it takes and the
// content filter (a Dapr CEL expression) its events must also match, if any.
type Route struct {
	Message string
	When    string
}

// ReadRoutes strictly reads a consuming record's routes — its subscription:
// a list of objects, each naming a message, no message twice, the record's
// subscribes message among them. A record without routes, or with an empty
// list, subscribes one message through the scalar subscribes value — one
// route with no filter.
func ReadRoutes(e ScaffoldEntry) ([]Route, error) {
	raw, ok := e.Values[KeyRoutes]
	if list, isList := raw.([]any); isList && len(list) == 0 {
		ok = false
	}
	if !ok {
		message, err := ReadSubscribesMessage(e)
		if err != nil {
			return nil, err
		}
		return []Route{{Message: message}}, nil
	}
	record := filepath.Join(e.Path, filepath.FromSlash(ScaffoldRelPath))
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: values.%s must be a list of routes", record, KeyRoutes)
	}
	routes := make([]Route, 0, len(list))
	seen := map[string]bool{}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: values.%s[%d] has type %T, expected an object", record, KeyRoutes, i, item)
		}
		message, _ := m[KeyMessage].(string)
		if message == "" {
			return nil, fmt.Errorf("%s: values.%s[%d].%s is missing", record, KeyRoutes, i, KeyMessage)
		}
		if seen[message] {
			return nil, fmt.Errorf("%s: values.%s routes message %q twice; give it one route, with one filter", record, KeyRoutes, message)
		}
		seen[message] = true
		when, _ := m[KeyWhen].(string)
		routes = append(routes, Route{Message: message, When: when})
	}
	// The pipeline is typed by subscribes, so a subscription leaving it out
	// would deliver only messages the code was not written for.
	if subscribes, ok := SoftValue(e.Values, KeySubscribes); ok && !seen[subscribes] {
		return nil, fmt.Errorf("%s: values.%s does not route the subscribed message %q\nadd a route for it, or change subscribes to the message the pipeline handles", record, KeyRoutes, subscribes)
	}
	return routes, nil
}

// ReadDefault reads what a subscribing record does with the events no route
// matches. The loader's code acknowledges them, so a record without the key
// means DefaultIgnore; a record that carries it states what its code does.
func ReadDefault(e ScaffoldEntry) (string, error) {
	v, err := RecordValueDefault(e, KeyDefault, DefaultIgnore)
	if err != nil {
		return "", err
	}
	if v != DefaultDeadLetter && v != DefaultIgnore {
		return "", fmt.Errorf("%s: values.%s is %q, expected %q or %q",
			filepath.Join(e.Path, filepath.FromSlash(ScaffoldRelPath)), KeyDefault, v, DefaultDeadLetter, DefaultIgnore)
	}
	return v, nil
}

// ReadChannel reads the topic a record publishes or subscribes on, when it
// overrides the default (the message name); empty when it does not.
func ReadChannel(e ScaffoldEntry) (string, error) {
	if _, ok := e.Values[KeyChannel]; !ok {
		return "", nil
	}
	return RecordValue(e, KeyChannel)
}

// MessageValue is the scalar message value as writers store it in the
// scaffold record: the message identity verbatim, no snapshot fields.
func MessageValue(message string) string { return message }
