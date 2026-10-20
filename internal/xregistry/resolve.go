package xregistry

import (
	"fmt"
	"sort"
	"strings"
)

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
