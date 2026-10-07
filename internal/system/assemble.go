package system

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/template"
)

// ErrNoComponents is returned when the workspace scan finds no scaffold
// record `sys create` can assemble into a component.
var ErrNoComponents = errors.New("no assemblable integration scaffolds found")

// Assemble classifies workspace scaffold records into a system model:
// shared-library records become the referenced contract project, records
// with a kind in the blockParsers registry become components, and
// everything else is skipped through warnf. The returned model's
// Name/ProjectName/SystemClass are left empty — the caller fills them
// around the template render.
func Assemble(entries []template.ScaffoldEntry, warnf func(format string, args ...any)) (*Model, error) {
	var (
		components []Component
		shared     []SharedLibrary
		byAppID    = map[string]string{}   // appId -> scaffold dir
		byTopic    = map[TopicKey]Topic{}  // key -> first-seen topic
		firstDir   = map[TopicKey]string{} // key -> dir that defined it
		byPort     = map[string]string{}   // port -> scaffold dir
	)

	for _, e := range entries {
		switch {
		case e.Role == template.RoleSharedLibrary:
			name, err := template.RecordValue(e, template.KeyName)
			if err != nil {
				return nil, err
			}
			shared = append(shared, SharedLibrary{Path: e.Path, Name: name})
			continue
		case e.Role == template.RoleSystemHost:
			warnf("skipping %s: an existing system host", e.Path)
			continue
		case e.BlockKind == "":
			warnf("skipping %s: scaffold record has no block kind — re-scaffold with a newer template release to include it", e.Path)
			continue
		}

		parse, ok := blockParsers[e.BlockKind]
		if !ok {
			warnf("skipping %s: unsupported block kind %q (sys create assembles %s)", e.Path, e.BlockKind, strings.Join(supportedKinds(), ", "))
			continue
		}

		appID, err := template.RecordValue(e, template.KeyAppID)
		if err != nil {
			return nil, err
		}
		c := Component{AppID: appID, Kind: e.BlockKind, Path: e.Path}
		if err := parse(e, &c); err != nil {
			return nil, err
		}
		if c.missingPort {
			warnf("%s: scaffold record has no port — re-scaffold with a newer template release to wire From/To", e.Path)
		}

		if prev, ok := byAppID[appID]; ok {
			return nil, fmt.Errorf("duplicate component name %q: declared by both %s and %s (component names must be unique in a system)", appID, prev, e.Path)
		}
		byAppID[appID] = e.Path

		for _, port := range c.Ports {
			if prev, ok := byPort[port]; ok {
				return nil, fmt.Errorf("duplicate port %q: declared by both %s and %s (each block gets its own port; rename one in the record's values)", port, prev, e.Path)
			}
			byPort[port] = e.Path
		}

		components = append(components, c)
	}

	if err := resolveSubscriptionChannels(components); err != nil {
		return nil, err
	}
	for i := range components {
		c := &components[i]
		if c.Topic == nil {
			continue
		}
		key := *c.Topic
		if seen, ok := byTopic[key]; ok {
			// A topic several messages share carries several contracts: it
			// has none of its own, each message keeps its own (Messages).
			// Two producers of one message disagreeing on its contract is
			// the real conflict, refused in resolveSubscriptionChannels.
			if seen.Contract != "" && c.topicContract != "" && seen.Contract != c.topicContract {
				seen.Contract = ""
				byTopic[key] = seen
			}
			continue
		}
		byTopic[key] = Topic{TopicKey: key, Contract: c.topicContract}
		firstDir[key] = c.Path
	}

	if len(components) == 0 {
		return nil, fmt.Errorf("%w in this directory\nScaffold integrations first ('intropy int create <kind> -n <Name> ...'), then run 'intropy sys create' from the workspace root", ErrNoComponents)
	}
	// Zero shared libraries is a valid workspace: a topic-free system never
	// references contracts, and a topic-bearing one gets them from the host
	// template's dependency. More than one is ambiguity the template cannot
	// resolve, so it stays an error regardless of the system's shape.
	if len(shared) > 1 {
		dirs := make([]string, len(shared))
		for i, s := range shared {
			dirs[i] = s.Path
		}
		return nil, fmt.Errorf("found %d shared contract projects (%s); a system host references exactly one — remove or consolidate before re-running", len(shared), strings.Join(dirs, ", "))
	}

	topics := make([]Topic, 0, len(byTopic))
	for _, t := range byTopic {
		topics = append(topics, t)
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Pubsub != topics[j].Pubsub {
			return topics[i].Pubsub < topics[j].Pubsub
		}
		return topics[i].Name < topics[j].Name
	})

	ports := make([]Port, 0, len(byPort))
	for name := range byPort {
		ports = append(ports, Port{Name: name})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Name < ports[j].Name })

	model := &Model{Components: components, Topics: topics, Ports: ports, Messages: aggregateMessages(components)}
	if len(shared) == 1 {
		model.Shared = &shared[0]
	}
	return model, nil
}

// resolveSubscriptionChannels gives every component a concrete channel:
// a publisher uses the system pubsub on the topic its record declares (the
// message name by default), and a subscriber the topic its record declares,
// else its first route's producer's.
//
// A half-wired subscription — nothing publishes a routed message — is a hard
// error naming the message and the subscribing record: the system would
// render a loader listening to nothing. So is a routed message published on
// another topic than the subscriber's: a loader consumes exactly one topic.
func resolveSubscriptionChannels(components []Component) error {
	type publisher struct {
		key      TopicKey
		contract string
		path     string
		appID    string
	}
	channels := map[string]*publisher{}
	for i := range components {
		c := &components[i]
		if c.Message == nil || c.Message.Kind != MessagePublish || c.Message.Name == "" {
			continue
		}
		if c.Topic == nil {
			c.topicContract = c.Message.Contract
			c.Topic = &TopicKey{Pubsub: template.DefaultPubsub, Name: c.Message.Name}
		}
		key := *c.Topic
		if r, ok := channels[c.Message.Name]; ok {
			if r.key != key {
				return fmt.Errorf("message %q is published on conflicting channels: %s on pubsub %q/topic %q (%s) vs pubsub %q/topic %q (%s)",
					c.Message.Name, r.appID, r.key.Pubsub, r.key.Name, r.path, key.Pubsub, key.Name, c.Path)
			}
			if r.contract != "" && c.Message.Contract != "" && r.contract != c.Message.Contract {
				return fmt.Errorf("message %q is published with conflicting contracts: %q (%s) vs %q (%s)",
					c.Message.Name, r.contract, r.path, c.Message.Contract, c.Path)
			}
			continue
		}
		channels[c.Message.Name] = &publisher{key: key, contract: c.Message.Contract, path: c.Path, appID: c.AppID}
	}

	for i := range components {
		c := &components[i]
		if c.Message == nil || c.Message.Kind != MessageSubscribe || c.Topic != nil {
			continue
		}
		for n, route := range c.Routes {
			r, ok := channels[route.Message]
			if !ok {
				return fmt.Errorf("message %q is subscribed by %s but no component in this workspace publishes it\ndeclare it with values.publishes in the producing record", route.Message, c.Path)
			}
			if c.Topic == nil {
				topic := c.channel
				if topic == "" {
					topic = r.key.Name
				}
				c.Topic = &TopicKey{Pubsub: r.key.Pubsub, Name: topic}
			}
			if r.key != *c.Topic {
				hint := fmt.Sprintf("set values.%s in %s to %q", template.KeyChannel, c.Path, r.key.Name)
				if n > 0 {
					hint = fmt.Sprintf("publish every routed message on one topic: set values.%s in the producing records (%s publishes on %q)", template.KeyChannel, r.appID, r.key.Name)
				}
				return fmt.Errorf("%s routes message %q, which %s publishes on topic %q, but %s consumes topic %q — a loader consumes one topic\n%s",
					c.AppID, route.Message, r.appID, r.key.Name, c.AppID, c.Topic.Name, hint)
			}
		}
	}
	return nil
}

// aggregateMessages builds the system message list from publish declarations,
// first seen wins, sorted by name. Subscribers name the same message; the
// subscription is visible through ComponentEntry.
func aggregateMessages(components []Component) []Message {
	seen := map[string]bool{}
	var out []Message
	for _, c := range components {
		if c.Message == nil || c.Message.Kind != MessagePublish {
			continue
		}
		m := Message{
			Name:      c.Message.Name,
			Type:      c.Message.Name,
			Contract:  c.Message.Contract,
			Publisher: c.AppID,
		}
		if c.Topic != nil {
			m.Pubsub, m.Topic = c.Topic.Pubsub, c.Topic.Name
		}
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
