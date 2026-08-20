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
  /** Root-relative directory of the system ("." when rooted at the workspace).
   *  The unique system key — two directories can declare the same `system`
   *  name — and the join key to a declared Topology's `path`. */
  systemPath?: string
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
  /** The served root's directory name — labels the workspace pseudo-system. */
  workspace?: string
}

/** One declared system: the directory holding a system-host scaffold
 *  (root-relative, the same identifier space as Integration.systemPath) and
 *  the name the host declares. */
export interface SystemInfo {
  path: string
  name: string
}

/** The POST /api/systems/{path} payload: what the host sync did. "update"
 *  folded orphans into an existing host, "create" assembled a new one,
 *  "none" found nothing to add and wrote nothing. */
export interface SystemSyncResponse {
  action: 'update' | 'create' | 'none'
  hostDir?: string
  system?: string
  added?: string[]
  /** Declared components whose scaffold is gone — kept as declared. */
  kept?: string[]
  diagnostics?: string[]
}

// Deployment state (internal/deploy). The server obtains each record by running
// the `deploy status` command with a JSON writer and passing its result through
// unchanged, so everything below is that command's own output — including the
// prose in `summary` and `notes`, which must be rendered as served rather than
// re-derived from `environments`.

/** How one overlay pins one image. */
export interface ResultPin {
  image: string
  /** The digest this replaced. Only present on a deployment's own result. */
  previous?: string
  digest: string
  /** The tag the digest was resolved from, or the tag pinned instead of a
   *  digest. Empty when the digest came from a release manifest. */
  tag?: string
}

/** One environment's row of the deployment ladder. */
export interface EnvironmentStatus {
  environment: string
  appName: string
  overlayPath: string
  /** False means there is no readable overlay here; `reason` says why and
   *  everything below is empty. On its own it never means "not deployed". */
  onboarded: boolean
  reason?: string
  /** The version this environment runs. Absent when it was deployed from a
   *  commit rather than a release, in which case `sourceCommit` stands in. */
  release?: string
  sourceCommit?: string
  /** Every image the component declares, in declared order. `digest` is empty
   *  when the overlay pins a tag or nothing — `tag` says which. */
  pins?: ResultPin[]
  /** The GitOps commit that last changed this overlay, and when it landed.
   *  Both absent when the overlay path has no history. */
  revision?: string
  /** RFC 3339. */
  deployedAt?: string
  syncPolicy: 'auto' | 'manual' | string
  /** What ArgoCD reports. Absent when it could not be read — which says
   *  nothing at all about what the overlay pins. */
  syncStatus?: string
  healthStatus?: string
  syncedRevision?: string
  /** What ArgoCD observed running in the cluster, as opposed to what the
   *  overlay pins. Absent under the same rule as `syncStatus`: its absence
   *  says nothing about the overlay or the cluster. */
  liveImages?: string[]
  /** A committed overlay change ArgoCD has not applied. For a `manual`
   *  environment that is an unspent gate at rest, not a fault. */
  pending: boolean
}

export interface StatusResult {
  component: string
  domain: string
  system: string
  /** Declared kind. "shared" holds system-level manifests and no image, so its
   *  empty digests are by design rather than evidence of never deploying. */
  kind: 'service' | 'shared' | string
  /** In promotion order: the last entry is the furthest downstream. */
  environments: EnvironmentStatus[]
  /** Every onboarded environment pins the identical digest for every image —
   *  what makes "prod runs the bits staging tested" a fact. */
  consistent: boolean
  /** The command's own sentence about what the environments collectively run.
   *  Render as served: `consistent` is a bool and the interesting cases are
   *  not — "these run different bits" and "there is nothing to compare" are
   *  both false and mean very different things. */
  summary: string
  /** The command's qualifications: an environment that could not be read, one
   *  pinning a tag, one waiting on a sync gate. */
  notes?: string[]
  /** The promotion graph restricted to this component's environments: for each
   *  environment, the ones a promotion into it may draw from. Carried on the
   *  result because deploy.yaml lives in the GitOps repository, which a
   *  consumer without a session cannot read. */
  promotesFrom?: Record<string, string[]>
}

// Catalog entry (internal/dashboard/catalog.go). Header identity and contracts
// come from the cached system topology when it has the component, and from the
// scaffold record — provenance only, it can go stale — otherwise. Deployment
// state is deliberately not embedded; it stays on /api/deploy/{path}, which
// costs a GitOps checkout refresh.

/** One end of a pub/sub wire a component sits on. */
export interface ContractEdge {
  pubsub: string
  topic: string
  /** The topic's contract shortName when the registry carries it, the raw
   *  contract name otherwise — the flow view's lookup rule. */
  contract?: string
  /** Every component the topic declares on each end of the wire — a contract
   *  shown as the connection between components, not a name. */
  publishers?: string[]
  subscribers?: string[]
}
/** One finding about the integration's place in the system graph. */
export interface CatalogCheck {
  severity: 'warn' | 'info'
  message: string
}

/** Why contracts are unknown, when they are: the catalog's join of the
 *  integration against its system's declared graph. */
export type GraphStatus =
  | 'matched'
  | 'no-topology'
  | 'not-in-graph'
  | 'topology-error'
  | 'pending'

export interface CatalogEntry {
  /** Graph name when matched, scaffold record name otherwise. */
  component: string
  /** Topology block kind; absent in every non-matched state. */
  kind?: string
  system?: string
  /** System directory the topology record joins on — for matching the
   *  contract registry when fetching /api/topology. */
  systemPath?: string
  publishes?: ContractEdge[]
  subscribes?: ContractEdge[]
  /** "owner/repo" from the scaffold record — provenance only. */
  repository?: string
  graphStatus: GraphStatus
  /** True while the first topology computation is in flight: the header
   *  renders from scaffold data and contracts are unknown. */
  topologyPending?: boolean
  /** Graph-derived findings, warns before infos. */
  checks?: CatalogCheck[]
}

/** The /api/deploy/{path} payload: what the deploy status command said about one
 *  integration, or why it could not say anything. */
export interface DeployState {
  status?: StatusResult
  /** The command's message, verbatim — an unconfigured GitOps repository, a
   *  component name matching several, a checkout another deploy is holding.
   *  None of these is a statement about the integration; only `status` is. */
  error?: string
  /** What the command wrote to stderr: which repository it refreshed, and any
   *  environment ArgoCD could not be read for. Provenance, not failure. */
  diagnostics?: string[]
  /** RFC 3339 — when the command ran. */
  readAt: string
}

// Declared system topology (internal/topology). The server obtains each
// record by running the system host's `graph` verb (apiVersion
// topology.intropy.io/v1) and caches the result until an explicit refresh.
// Wiring is inlined on each component — the topics it subscribes to and
// publishes on and the ports it uses — while the top-level `topics` and
// `ports` sections are the lookup tables those inline references resolve
// against. There is no fallback: a system whose host cannot produce a record
// appears in `errors` instead of `topologies`.

/** A component's subscription to a pub/sub topic. */
export interface TopicRef {
  pubsub: string
  topic: string
}

/** A component's output onto a pub/sub topic. */
export interface Publication {
  pubsub: string
  topic: string
}

/** A component's use of an external port. */
export interface PortUse {
  port: string
  /** "in" (external → component) or "out" (component → external). */
  direction: 'in' | 'out'
}

export interface TopologyComponent {
  name: string
  /** Block type: extractor, loader, transactional-integration, … */
  kind: string
  subscribes?: TopicRef[]
  publishes?: Publication[]
  ports?: PortUse[]
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

/** An external system integration point. The name is its whole identity —
 *  the deployed Dapr binding type is environment-owned deployment
 *  configuration, never part of the record. */
export interface TopologyPort {
  name: string
  externalSystem?: string
  directions?: Array<'in' | 'out'>
  usedBy?: string[]
}

/** A message contract in the system's registry, keyed by `name` — the same
 *  fully-qualified type name TopologyTopic.contract references. */
export interface Contract {
  name: string
  /** "event" for pub/sub message contracts. */
  kind?: string
  /** Bare type name — the join key to a scaffold record's values.contract. */
  shortName?: string
  mediaType?: string
  /** Hash of the schema's canonical form; equal fingerprints mean equal
   *  shapes across systems. */
  fingerprint?: string
  /** JSON Schema as the host emitted it, passed through verbatim. */
  schema?: JsonSchema
}

/** The subset of JSON Schema the contract field tree renders. Anything it
 *  does not recognize falls back to a raw type label, never an error. */
export interface JsonSchema {
  type?: string | string[]
  format?: string
  properties?: Record<string, JsonSchema>
  required?: string[]
  items?: JsonSchema
  enum?: unknown[]
  $ref?: string
  $defs?: Record<string, JsonSchema>
}

/** One field or column of an external payload, as its author described it.
 *  `type` is loose by design — documentation, not a validatable schema. */
export interface MessageDocField {
  name: string
  position?: number
  type?: string
  required?: boolean
  notes?: string
}

/** A short inline payload excerpt. The server only serves samples whose
 *  author explicitly asserted the redaction check. */
export interface MessageDocSample {
  inline: string
  redacted?: boolean
}

/** An authored description of the payload a port carries (the flat file from
 *  an SFTP drop, the ad-hoc CSV export), read from the system's
 *  messages/<port>.md sidecar. Documentation, not enforcement. */
export interface MessageDoc {
  format?: string
  delimiter?: string
  encoding?: string
  filePattern?: string
  frequency?: string
  contact?: string
  lastReviewed?: string
  fields?: MessageDocField[]
  sample?: MessageDocSample
  /** Free prose from the doc's Markdown body. */
  body?: string
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
  ports?: TopologyPort[]
  /** Message contract registry topics reference by contract name. Absent
   *  until the system host's topology library emits it. */
  contracts?: Contract[]
  /** Contract surfaces — parsed but not yet rendered (shape not finalized). */
  apis?: unknown[]
  /** Authored port payload descriptions, keyed by port name. CLI-merged
   *  enrichment from messages/<port>.md — not part of the host-declared
   *  topology, and re-read on every request. */
  messageDocs?: Record<string, MessageDoc>
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
    // Carry the status so callers can branch on it — the create forms offer
    // force on a 409 (non-empty output directory).
    const err = new Error(message) as Error & { status?: number }
    err.status = res.status
    throw err
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
  /** Every declared system — including hosts with no blocks yet, which
   *  /api/flow cannot surface (it only carries systems through their blocks). */
  systems: () => getJSON<SystemInfo[]>('/api/systems'),
  /** Re-assemble one system's host the way the CLI would: `sys update` when
   *  the directory has a host, `sys create` when it does not. Path "." is
   *  the workspace root. */
  syncSystem: (path: string, force = false) =>
    requestJSON<SystemSyncResponse>(`/api/systems/${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ force }),
    }),
  /** Every declared system topology, cached from the hosts' graph verbs. */
  topologies: () => getJSON<TopologyReport>('/api/topology'),
  /** Re-run every host's graph verb and return the fresh report. */
  refreshTopologies: () =>
    requestJSON<TopologyReport>('/api/topology/refresh', { method: 'POST' }),
  /** One integration's catalog entry: header facts and graph-derived checks. */
  catalog: (path: string) => getJSON<CatalogEntry>(`/api/catalog/${path}`),
  /** One integration's deployment state, read once and reused by the server. */
  deployState: (path: string) => getJSON<DeployState>(`/api/deploy/${path}`),
  /** Re-run `deploy status` for one integration and return the fresh result —
   *  how you pick up a deploy just made from another terminal. */
  refreshDeployState: (path: string) =>
    requestJSON<DeployState>(`/api/deploy/${path}`, { method: 'POST' }),
  health: () => getJSON<Health>('/api/health'),

  // Template endpoints (internal/dashboard/templates.go). They wrap the
  // `template` and `int create` commands: the library release the server
  // fetched is the release the form renders against and the run creates from.
  listTemplates: () => getJSON<TemplateList>('/api/templates'),
  getTemplate: (name: string) => getJSON<TemplateDetail>(`/api/templates/${name}`),
  /** Render a template into the workspace — the Run button's `int create`. */
  createTemplate: (name: string, req: CreateRequest) =>
    requestJSON<CreateResponse>(`/api/templates/${name}/create`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
}

// Template API (internal/dashboard/templates.go). The shapes mirror the Go
// JSON contract the endpoints serve: template.List, template.DescribeResult,
// and template.CreateResult.

/** One list entry with the manifest metadata create surfaces filter on —
 *  notably the intropy.dev/* labels a flow-view slot selects templates by. */
export interface TemplateSummary {
  name: string
  title?: string
  description?: string
  labels?: Record<string, string>
}

/** The /api/templates payload: one library release's template names, plus
 *  per-template metadata in `entries` (additive beside the bare names). */
export interface TemplateList {
  owner: string
  repo: string
  version: string
  templates: string[]
  entries?: TemplateSummary[]
}

/** One parameter of a template's schema, in YAML declaration order. Mirrors
 *  template.FieldSpec — the form renders from these, never the raw schema. */
export interface TemplateField {
  name: string
  title?: string
  description?: string
  type: 'string' | 'boolean' | 'integer' | 'number'
  enum?: unknown[]
  default?: unknown
  pattern?: string
  required: boolean
}

/** The /api/templates/{name} payload: the `template show -o json` document.
 *  `fields` is the parameter list in YAML declaration order; `parameters` is
 *  the raw JSON Schema. */
export interface TemplateDetail {
  template: string
  title?: string
  description?: string
  tags?: string[]
  labels?: Record<string, string>
  owner: string
  repo: string
  version: string
  parameters: Record<string, unknown>
  dependencies?: { template: string; output: string }[]
  fields: TemplateField[]
}

/** The POST body for create. `name` folds into values.name and becomes the
 *  output directory under `dir` (the workspace root when omitted or "."), as
 *  `int create --name` does. `dir` must be an existing root-relative
 *  directory — creating into a system, not inventing directory trees. */
export interface CreateRequest {
  name: string
  dir?: string
  values: Record<string, unknown>
  force?: boolean
}

/** The 201 payload: template.CreateResult with a root-relative outputDir. */
export interface CreateResponse {
  template: string
  owner: string
  repo: string
  version: string
  outputDir: string
  values: Record<string, unknown>
  dependencies?: { template: string; outputDir: string; action: string }[]
  diagnostics?: string[]
}
