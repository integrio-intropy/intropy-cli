package topology

import (
	"encoding/json"
	"slices"
)

// Message is one message of the messagegroups section: its name (its
// CloudEvent type), its contract, the channel it travels and the components
// on each end.
type Message struct {
	Name        string   `json:"name"`
	Contract    string   `json:"contract,omitempty"`
	Channel     Channel  `json:"channel"`
	Publishers  []string `json:"publishers,omitempty"`
	Subscribers []string `json:"subscribers,omitempty"`
}

// Channel is the pub/sub topic a message travels.
type Channel struct {
	PubSub string `json:"pubsub"`
	Topic  string `json:"topic"`
}

// messageGroup is the one part of a messagegroups entry the CLI reads. The
// section stays opaque on Topology (see MessageGroups): a group whose shape
// does not match contributes no messages instead of failing the record.
type messageGroup struct {
	Messages []Message `json:"messages"`
}

// Messages returns every message the messagegroups section declares, in
// section order. A record without the section has none.
func (t *Topology) Messages() []Message {
	var out []Message
	for _, raw := range t.MessageGroups {
		var g messageGroup
		if json.Unmarshal(raw, &g) != nil {
			continue
		}
		out = append(out, g.Messages...)
	}
	return out
}

// Message returns the declared message named name, if there is one.
func (t *Topology) Message(name string) (Message, bool) {
	for _, m := range t.Messages() {
		if m.Name == name {
			return m, true
		}
	}
	return Message{}, false
}

// TopicContracts returns the distinct contracts carried on a topic, sorted:
// those of the messages the messagegroups section puts on its channel, or —
// for a record older than message wiring — the topic's own contract.
func (t *Topology) TopicContracts(pubsub, topic string) []string {
	var out []string
	for _, m := range t.Messages() {
		if m.Channel.PubSub == pubsub && m.Channel.Topic == topic && m.Contract != "" &&
			!slices.Contains(out, m.Contract) {
			out = append(out, m.Contract)
		}
	}
	if len(out) == 0 {
		for _, tp := range t.Topics {
			if tp.PubSub == pubsub && tp.Topic == topic && tp.Contract != "" {
				out = append(out, tp.Contract)
			}
		}
	}
	slices.Sort(out)
	return out
}
