package xregistry

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Channel is one producing channel: the pub/sub component and topic an
// event flows through, as split from the endpoint's <pubsub>/<topic>
// channel string.
type Channel struct {
	Pubsub   string `json:"pubsub"`
	Topic    string `json:"topic"`
	Endpoint string `json:"endpoint,omitempty"`
}

// ResolvedMessage is the wiring --subscribe writes into a scaffold record:
// the message identity, its CloudEvents type, the channels the message can
// arrive on, and the schema pin.
type ResolvedMessage struct {
	// Message is the message id, which for these registries doubles as the
	// CloudEvents type — the registry constrains envelopemetadata.type to
	// the same dotted name. Type carries the envelope value verbatim; the
	// two are distinct fields so a registry that ever diverges them stays
	// representable.
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Group   string `json:"group"`

	// Channels is the set of producer endpoints' channels for the message's
	// group, split and sorted. Exactly one is the subscribe channel; more
	// is AmbiguousProducerError, none is NoProducerError.
	Channels []Channel `json:"channels,omitempty"`

	// DataSchema is the logical schema xid from the message definition;
	// DataSchemaURL pins the schema's default version — an immutable URL —
	// at subscribe time.
	DataSchema    string `json:"dataschema,omitempty"`
	DataSchemaURL string `json:"dataschemaurl,omitempty"`

	Envelope string `json:"envelope,omitempty"`
}

// ResolveMessage resolves one message reference — a bare message id or a
// full /messagegroups/<gid>/messages/<mid> xid — against the export
// document into subscription wiring. All joins are in memory: nothing but
// the export is fetched.
//
// The resolution the caller cannot do without: message → group → producer
// endpoints → channel split, plus dataschemaxid → default version URL.
func ResolveMessage(doc *Export, ref string) (*ResolvedMessage, error) {
	msg, group, err := findMessage(doc, ref)
	if err != nil {
		return nil, err
	}
	dflt := msg.DefaultMessage()
	if dflt == nil {
		return nil, &NoDefaultVersionError{Ref: ref, Group: group.ID}
	}

	res := &ResolvedMessage{
		Message:  msg.ID,
		Group:    group.ID,
		Envelope: dflt.Envelope,
	}
	if attr, ok := dflt.EnvelopeMetadata["type"]; ok {
		if s, ok := attr.Value.(string); ok {
			res.Type = s
		}
	}
	res.DataSchema = dflt.DataSchemaXID
	if res.DataSchema != "" {
		xid, err := DefaultSchemaVersionXID(doc, res.DataSchema)
		if err != nil {
			return nil, err
		}
		// Root-relative xid: the caller (Client.Resolve) joins it with the
		// base URL; a bare ResolveMessage on a hand-built doc keeps the xid.
		res.DataSchemaURL = xid
	}

	channels, err := producerChannels(doc, group.ID)
	if err != nil {
		return nil, err
	}
	res.Channels = channels
	return res, nil
}

// SubscribeChannel picks the one channel of a resolved message. The choice
// is the caller's only when several producers carry the group on different
// channels — the interactive pick list and the --no-input error both
// consume the full Channels list, so it is kept there.
func (r *ResolvedMessage) SubscribeChannel() (Channel, error) {
	switch len(r.Channels) {
	case 0:
		return Channel{}, &NoProducerError{Group: r.Group}
	case 1:
		return r.Channels[0], nil
	default:
		return Channel{}, &AmbiguousProducerError{Ref: r.Message, Channels: r.Channels}
	}
}

// findMessage locates a message by reference. A bare id is matched across
// groups and must name exactly one message — two groups reusing one id is
// ambiguity the reference alone cannot resolve.
// FindMessage locates one message by reference — a bare message id
// (unique across groups) or a full /messagegroups/<gid>/messages/<mid>
// xid — and returns it with its group. Exported for display surfaces; the
// ambiguity and not-found failures are the typed errors.
func FindMessage(doc *Export, ref string) (*Message, *MessageGroup, error) {
	return findMessage(doc, ref)
}

func findMessage(doc *Export, ref string) (*Message, *MessageGroup, error) {
	if ref == "" {
		return nil, nil, fmt.Errorf("message reference is empty")
	}
	if xid, ok := strings.CutPrefix(ref, "/messagegroups/"); ok {
		gid, mid, ok := strings.Cut(xid, "/messages/")
		if !ok || gid == "" || mid == "" {
			return nil, nil, fmt.Errorf("message reference %q is not a message id or /messagegroups/<gid>/messages/<mid> xid", ref)
		}
		g, ok := doc.MessageGroups[gid]
		if !ok {
			return nil, nil, &MessageNotFoundError{Ref: ref}
		}
		m, ok := g.Messages[mid]
		if !ok {
			return nil, nil, &MessageNotFoundError{Ref: ref}
		}
		return &m, &g, nil
	}

	var found *Message
	var group *MessageGroup
	for i := range doc.MessageGroups {
		g := doc.MessageGroups[i]
		if m, ok := g.Messages[ref]; ok {
			if found != nil {
				xids := messageGroupXIDs(doc, ref)
				return nil, nil, fmt.Errorf("message reference %q is ambiguous: declared in groups %s", ref, strings.Join(xids, ", "))
			}
			mCopy, gCopy := m, g
			found, group = &mCopy, &gCopy
		}
	}
	if found == nil {
		return nil, nil, &MessageNotFoundError{Ref: ref}
	}
	return found, group, nil
}

// messageGroupXIDs names the groups carrying ref, for the ambiguity error.
func messageGroupXIDs(doc *Export, ref string) []string {
	var out []string
	for _, g := range doc.MessageGroups {
		if _, ok := g.Messages[ref]; ok {
			out = append(out, "/messagegroups/"+g.ID)
		}
	}
	sort.Strings(out)
	return out
}

// producerChannels joins the group to its producer endpoints and splits
// each channel. Endpoints whose usage omits "producer" are subscriptions
// and consumers, never the resolution answer.
func producerChannels(doc *Export, groupID string) ([]Channel, error) {
	want := "/messagegroups/" + groupID
	var channels []Channel
	for _, e := range doc.Endpoints {
		if !slicesContain(e.Usage, "producer") {
			continue
		}
		if !slicesContain(e.MessageGroups, want) {
			continue
		}
		pubsub, topic, err := SplitChannel(e.Channel)
		if err != nil {
			return nil, &ChannelFormatError{Endpoint: e.ID, Channel: e.Channel, err: err}
		}
		channels = append(channels, Channel{Pubsub: pubsub, Topic: topic, Endpoint: e.ID})
	}
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Pubsub != channels[j].Pubsub {
			return channels[i].Pubsub < channels[j].Pubsub
		}
		return channels[i].Topic < channels[j].Topic
	})
	return channels, nil
}

// Resolve fetches the export and resolves one message reference into
// subscription wiring — the flow --subscribe runs. The schema pin is
// returned absolute: the pinned version URL must survive on its own once
// the record is committed, so it is joined with the base URL here.
func (c *Client) Resolve(ctx context.Context, ref string) (*ResolvedMessage, error) {
	doc, err := c.Export(ctx)
	if err != nil {
		return nil, err
	}
	res, err := ResolveMessage(doc, ref)
	if err != nil {
		return nil, err
	}
	if res.DataSchemaURL != "" {
		if u, err := c.base.Parse(strings.TrimPrefix(res.DataSchemaURL, "/")); err == nil {
			res.DataSchemaURL = u.String()
		}
	}
	return res, nil
}

// SplitChannel splits an endpoint channel into its pub/sub component and
// topic halves. The format belongs to the registry's conformance suite;
// this validates and fails naming the endpoint rather than accepting a
// half-parsed channel.
func SplitChannel(channel string) (pubsub, topic string, err error) {
	pubsub, topic, ok := strings.Cut(channel, "/")
	if !ok || strings.TrimSpace(pubsub) == "" || strings.TrimSpace(topic) == "" {
		return "", "", fmt.Errorf("channel %q is not <pubsub>/<topic>", channel)
	}
	return pubsub, topic, nil
}

// DefaultSchemaVersionXID pins a logical schema xid to its default
// version's xid — the immutable form a CloudEvent dataschema references.
// Exported for callers that already hold the export document and resolve
// display data (URLs, names) without a second round-trip.
func DefaultSchemaVersionXID(doc *Export, xid string) (string, error) {
	gid, sid, ok := splitSchemaXID(xid)
	if !ok {
		return "", &SchemaNotFoundError{XID: xid}
	}
	g, ok := doc.SchemaGroups[gid]
	if !ok {
		return "", &SchemaNotFoundError{XID: xid}
	}
	s, ok := g.Schemas[sid]
	if !ok {
		return "", &SchemaNotFoundError{XID: xid}
	}
	for _, v := range s.Versions {
		if v.IsDefault {
			return v.XID, nil
		}
	}
	return "", &SchemaNotFoundError{XID: xid}
}

// splitSchemaXID parses /schemagroups/<gid>/schemas/<sid>.
func splitSchemaXID(xid string) (gid, sid string, ok bool) {
	rest, ok := strings.CutPrefix(xid, "/schemagroups/")
	if !ok {
		return "", "", false
	}
	gid, sid, ok = strings.Cut(rest, "/schemas/")
	if !ok || gid == "" || sid == "" {
		return "", "", false
	}
	return gid, sid, true
}

func slicesContain(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
