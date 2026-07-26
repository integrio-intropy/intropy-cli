// Types mirror the Go JSON contract served by `intropy dashboard`
// (internal/dashboard). Integration matches template.ScaffoldEntry;
// IntegrationDetail adds the best-effort on-disk enrichment.

/** A dependency another template rendered alongside this integration. */
export interface DependencyRecord {
  template: string
  /** Slash-separated directory relative to the component root. */
  dir: string
}

export interface Integration {
  path: string
  schemaVersion: number
  template: string
  owner: string
  repo: string
  version: string
  /** Which way data crosses the system boundary, declared by the template
   *  (intropy.dev/data-flow label) and recorded in scaffold.json. */
  dataFlow?: 'in' | 'out' | 'both' | 'internal'
  /** Topology block kind the template scaffolds (intropy.dev/block-kind
   *  label), e.g. "extractor" or "loader". */
  blockKind?: string
  /** Support-project role (intropy.dev/template-role label). Projects with a
   *  role (system-host, shared-library) are filtered out of the catalog by
   *  the server; the field only appears on records read elsewhere. */
  role?: string
  /** Sibling projects this integration's template rendered as dependencies. */
  dependsOn?: DependencyRecord[]
  values: Record<string, unknown>
  /** Directory carrying the .intropy record; the integration's display name. */
  name: string
  /** Parent directory of the integration; absent directly under the root. */
  system?: string
  /** Parent directory of the system; absent when the system sits at the root. */
  domain?: string
}

export interface FileDoc {
  name: string
  content: string
}

/** A parsed Dapr component. `category` decides where the flow canvas draws it:
 *  `pubsub`/`state` inside the system boundary, `binding` outside — as a
 *  source when `direction` is `input`, a sink otherwise. */
export interface DaprComponent {
  name: string
  type: string
  category: 'pubsub' | 'state' | 'binding' | 'other'
  /** Normalized `direction` metadata from the component spec. Absent when
   *  undeclared — treated as unknown (rendered as a sink), never inferred. */
  direction?: 'input' | 'output' | 'input,output'
  file: string
}

export interface IntegrationDetail extends Integration {
  agents?: FileDoc
  readme?: FileDoc
  components?: DaprComponent[]
  pipelineSteps?: string[]
}

export interface Health {
  status: string
  version: string
}

// Declared system topology (internal/topology). The server obtains each
// record by running the system host's `graph` verb (apiVersion
// topology.intropy.io/v1) and caches the result until an explicit refresh.
// Wiring is inlined on each component — the topics it subscribes to and
// publishes on and the connectors it uses — while the top-level `topics` and
// `connectors` sections are the lookup tables those inline references resolve
// against. There is no fallback: a system whose host cannot produce a record
// appears in `errors` instead of `topologies`.

/** A component's subscription to a pub/sub topic. */
export interface TopicRef {
  pubsub: string
  topic: string
}

/** A component's output onto a pub/sub topic. */
export interface Publication {
  /** Component output port the message leaves through. */
  port?: string
  pubsub: string
  topic: string
}

/** A component's use of an external connector. */
export interface ConnectorUse {
  connector: string
  /** "in" (external → component) or "out" (component → external). */
  direction: 'in' | 'out'
}

export interface TopologyComponent {
  name: string
  /** Block type: extractor, loader, transactionalIntegration, … */
  kind: string
  subscribes?: TopicRef[]
  publishes?: Publication[]
  connectors?: ConnectorUse[]
  /** Contract surfaces — parsed but not yet rendered (shape not finalized). */
  provides?: unknown[]
  consumes?: unknown[]
}

/** A declared pub/sub topic: metadata for a (pubsub, topic) pair components
 *  reference. */
export interface TopologyTopic {
  pubsub: string
  topic: string
  contract?: string
  publishers?: string[]
  subscribers?: string[]
}

export interface TopologyTransport {
  type: string
  supportsInput: boolean
  supportsOutput: boolean
}

/** An external system integration point. */
export interface TopologyConnector {
  name: string
  externalSystem?: string
  transport: TopologyTransport
  directions?: Array<'in' | 'out'>
  usedBy?: string[]
}

export interface Topology {
  /** Root-relative system directory, same identifier space as Integration.path. */
  path: string
  apiVersion: string
  kind?: string
  /** System name (a bare string in v1). */
  system: string
  components?: TopologyComponent[]
  topics?: TopologyTopic[]
  connectors?: TopologyConnector[]
  /** Contract surfaces — parsed but not yet rendered (shape not finalized). */
  apis?: unknown[]
}

/** The /api/topology payload: every declared topology plus the per-host
 *  failures (a host whose graph verb failed, e.g. because it pre-dates the
 *  verb or does not build). */
export interface TopologyReport {
  topologies: Topology[]
  errors?: string[]
}

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init)
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body?.error) message = body.error
    } catch {
      // response had no JSON error body; keep the status line
    }
    throw new Error(message)
  }
  return (await res.json()) as T
}

const getJSON = <T,>(url: string) => requestJSON<T>(url)

export const api = {
  listIntegrations: () => getJSON<Integration[]>('/api/integrations'),
  getIntegration: (path: string) =>
    getJSON<IntegrationDetail>(`/api/integrations/${path}`),
  /** Every integration enriched with pipeline steps + Dapr components for the flow canvas. */
  flow: () => getJSON<IntegrationDetail[]>('/api/flow'),
  /** Every declared system topology, cached from the hosts' graph verbs. */
  topologies: () => getJSON<TopologyReport>('/api/topology'),
  /** Re-run every host's graph verb and return the fresh report. */
  refreshTopologies: () =>
    requestJSON<TopologyReport>('/api/topology/refresh', { method: 'POST' }),
  health: () => getJSON<Health>('/api/health'),
}
