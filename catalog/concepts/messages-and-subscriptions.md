---
type: Intropy Concept
title: Messages and subscriptions
description: How internal and external messages, subscribes, and publishes relate.
tags: [intropy, concepts, messaging]
status: draft
commands:
  - intropy message list
  - intropy message show
  - intropy int create --subscribe
sources:
  - id: xregistry-api
    resource: https://registry.intropy.io
    title: Intropy xRegistry API reference
  - id: xregistry-spec
    resource: https://xregistry.io
    title: xRegistry specification
---

# Purpose

<!-- One paragraph: wiring values the registry already owns (CloudEvents type,
pubsub, topic, schema) were typed by hand into template prompts; internal
events used a different vocabulary than cross-system events. One formal
vocabulary — message — replaces both. -->

# Concepts

| Concept | Meaning |
| --- | --- |
| Message | A message definition. Comes from the read-only xRegistry (external) or a producing component's `publishes` block (internal). |
| Subscribe | Written into a component's record as a `subscribe` block. An internal block names only the message; assembly resolves its channel from the producer. An external block carries the full snapshot (pubsub, topic, dataschema, pinned schema version URL). |
| Publishes | A producer's declaration: `message` (doubles as the CloudEvents type) plus the transitional `contract` (the .NET shared-project type name). |
| Channel | `<pubsub>/<topic>` — producer-declared on the registry, resolved from the publisher in assembly for internal subscriptions. |

# Invariants

- `sys create`/`sys update` never contact the registry; external
  subscriptions carry their own snapshot.
- Records written today use the block shape only. Legacy flat
  `topic`/`contract`/`pubsub` records stay functional and read as
  `publishes`/`subscribe` derived from the block kind (extractor publishes,
  loader subscribes).
- An internal subscription whose message no component publishes is an
  assembly error, not a rendered half-wired system.
- `dataschemaurl` pins the schema's default version at subscribe time;
  re-pinning is a future re-resolve flow.

# Examples

<!-- Show: message list output, one subscribe block in a scaffold record,
one publishes block, and the resulting messagegroup in the topology. -->

# Related knowledge

- [Intropy resource model](/concepts/intropy-resource-model.md)
- [State and ownership](/concepts/state-and-ownership.md)
