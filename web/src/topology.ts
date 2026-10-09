import type { Topology, TopologyMessage } from './api'

// Lookups over a declared topology's messages, mirroring internal/topology:
// a topic carries messages, each with its own contract, and a record older
// than message wiring gives the topic one contract of its own instead.

/** Every message the topology's messagegroups section declares. */
export function messages(topology: Topology): TopologyMessage[] {
  return (topology.messagegroups ?? []).flatMap((g) => g.messages ?? [])
}

/** The messages travelling one topic, in section order. */
export function topicMessages(topology: Topology, pubsub: string, topic: string): TopologyMessage[] {
  return messages(topology).filter((m) => m.channel?.pubsub === pubsub && m.channel?.topic === topic)
}

/** The distinct contracts carried on a topic, sorted: its messages', or the
 *  topic's own on an older record. */
export function topicContracts(topology: Topology, pubsub: string, topic: string): string[] {
  const fromMessages = topicMessages(topology, pubsub, topic)
    .map((m) => m.contract)
    .filter((c): c is string => !!c)
  if (fromMessages.length > 0) return [...new Set(fromMessages)].sort()
  const own = topology.topics?.find((t) => t.pubsub === pubsub && t.topic === topic)?.contract
  return own ? [own] : []
}
