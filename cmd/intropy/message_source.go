package main

// The registry half of the message commands: parse the export document
// into the shared MessageEntry shape. All joins happen in memory against
// one GET /export — the registry declares filtering and per-entity reverse
// lookups out of scope, so there is nothing smaller to fetch.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/integrio-intropy/intropy-cli/internal/xregistry"
)

const (
	messageSourceRegistry  = "registry"
	messageSourceWorkspace = "workspace"
)

// registryMessages reads the export into entries, optionally narrowed to
// one group. Resolution failures that block wiring (an ambiguous or
// unreachable subscription channel) do not block listing: the table shows
// the channels it can see.
func registryMessages(ctx context.Context, client *xregistry.Client, group string) ([]MessageEntry, error) {
	doc, err := client.Export(ctx)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(doc.MessageGroups))
	for id := range doc.MessageGroups {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []MessageEntry
	for _, gid := range ids {
		g := doc.MessageGroups[gid]
		if group != "" && gid != group {
			continue
		}
		for _, mid := range sortedMessageIDs(g) {
			m := g.Messages[mid]
			def := m.DefaultMessage()
			if def == nil {
				continue
			}
			e := MessageEntry{
				Message:  m.ID,
				Source:   messageSourceRegistry,
				Group:    gid,
				Type:     envelopeType(def),
				Envelope: def.Envelope,
				Channels: producerChannels(doc, gid),
			}
			if def.DataSchemaXID != "" {
				e.DataSchema = def.DataSchemaXID
				e.DataSchemaName = schemaName(def.DataSchemaXID)
				if xid, err := xregistry.DefaultSchemaVersionXID(doc, def.DataSchemaXID); err == nil {
					e.DataSchemaURL = joinURL(client.BaseURL(), xid)
				}
			}
			e.EnvelopeMeta = envelopeMetadata(def)
			out = append(out, e)
		}
	}
	return out, nil
}

// registryMessage finds exactly one message by reference — bare message id
// or /messagegroups/<gid>/messages/<mid> xid — for `message show`.
func registryMessage(ctx context.Context, client *xregistry.Client, ref string) (*MessageEntry, error) {
	doc, err := client.Export(ctx)
	if err != nil {
		return nil, err
	}
	msg, group, err := xregistry.FindMessage(doc, ref)
	if err != nil {
		return nil, err
	}
	def := msg.DefaultMessage()
	if def == nil {
		return nil, fmt.Errorf("message %q in group %s carries no definition attributes", ref, group.ID)
	}
	e := &MessageEntry{
		Message:  msg.ID,
		Source:   messageSourceRegistry,
		Group:    group.ID,
		Type:     envelopeType(def),
		Envelope: def.Envelope,
		Channels: producerChannels(doc, group.ID),
	}
	if def.DataSchemaXID != "" {
		e.DataSchema = def.DataSchemaXID
		e.DataSchemaName = schemaName(def.DataSchemaXID)
		if xid, err := xregistry.DefaultSchemaVersionXID(doc, def.DataSchemaXID); err == nil {
			e.DataSchemaURL = joinURL(client.BaseURL(), xid)
		}
	}
	e.EnvelopeMeta = envelopeMetadata(def)
	return e, nil
}

func sortedMessageIDs(g xregistry.MessageGroup) []string {
	ids := make([]string, 0, len(g.Messages))
	for id := range g.Messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// producerChannels lists the producer endpoints' channels for one group,
// split-validated and rendered as <pubsub>/<topic>. A malformed channel is
// skipped here: listing must show what is resolvable, and assembly or
// --subscribe is the surface that reports the malformed endpoint.
func producerChannels(doc *xregistry.Export, groupID string) []string {
	want := "/messagegroups/" + groupID
	var out []string
	for _, e := range doc.Endpoints {
		if !usageHas(e.Usage, "producer") || !usageHas(e.MessageGroups, want) {
			continue
		}
		if pubsub, topic, err := xregistry.SplitChannel(e.Channel); err == nil {
			out = append(out, pubsub+"/"+topic)
		}
	}
	sort.Strings(out)
	return out
}

func usageHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// envelopeType extracts the CloudEvents type from the envelope metadata.
func envelopeType(def *xregistry.MessageVersion) string {
	if attr, ok := def.EnvelopeMetadata["type"]; ok {
		if s, ok := attr.Value.(string); ok {
			return s
		}
	}
	return ""
}

// envelopeMetadata renders the envelope attributes sorted by name so the
// JSON document is stable across exports.
func envelopeMetadata(def *xregistry.MessageVersion) []kvHolder {
	names := make([]string, 0, len(def.EnvelopeMetadata))
	for n := range def.EnvelopeMetadata {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]kvHolder, 0, len(names))
	for _, n := range names {
		a := def.EnvelopeMetadata[n]
		kv := kvHolder{Name: n, Required: a.Required}
		if s, ok := a.Value.(string); ok {
			kv.Value = s
		} else if a.Value != nil {
			kv.Value = fmt.Sprint(a.Value)
		}
		out = append(out, kv)
	}
	return out
}

// schemaName renders the schema xid's resource tail for the table column.
func schemaName(xid string) string {
	if i := strings.LastIndex(xid, "/schemas/"); i >= 0 {
		rest := xid[i+len("/schemas/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return xid
}

// joinURL joins the registry base URL with a root-relative xid.
func joinURL(base, xid string) string {
	return strings.TrimSuffix(base, "/") + xid
}
