import { useEffect, useMemo, useState } from 'react'
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useNodesInitialized,
  useNodesState,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  api,
  type DaprComponent,
  type IntegrationDetail,
  type Topology,
  type TopologyReport,
} from '../api'
import {
  CloudIcon,
  CycleIcon,
  HardDriveIcon,
  HubIcon,
  InputIcon,
  MemoryIcon,
  OutputIcon,
  SyncAltIcon,
} from '../icons'
import { Combobox, type ComboOption } from './Combobox'

interface Props {
  selected: string | null
  onSelect: (path: string) => void
  /** Resolved app theme; drives React Flow's chrome (controls, minimap, edges). */
  theme: 'light' | 'dark'
}

// Layout geometry. The layout is deterministic: components and the topics
// they publish/subscribe flow left-to-right by declared depth inside the
// system boundary, and external systems (via connectors) sit outside it.
const NODE_W = 300
const NODE_H = 84
const COL_GAP = 28
const ROW_GAP = 28
const GROUP_HEADER = 44
const GROUP_PAD = 20
const INFRA_W = 200
const INFRA_H = 76
const EXT_W = 184
const EXT_H = 72
const EXT_GAP = 96
const EXT_VGAP = 24

const WORKSPACE_KEY = '__workspace__'

/** Which way data crosses the system boundary for a component. */
type DataFlow = 'in' | 'out' | 'both' | 'internal'

// Boundary direction by declared component kind, used only to pick the card's
// directional glyph — never to invent wiring; every edge on the canvas comes
// from the declared topology.
const KIND_DATA_FLOW: Record<string, DataFlow> = {
  extractor: 'in',
  loader: 'out',
  transactional: 'both',
  transactionalintegration: 'both',
  aggregator: 'internal',
}

// dataFlowFor resolves a component kind to its boundary direction, ignoring
// case and hyphens (the topology library kebab-cases kinds, e.g.
// "transactional-integration").
function dataFlowFor(kind: string): DataFlow | undefined {
  return KIND_DATA_FLOW[kind.toLowerCase().replace(/-/g, '')]
}

// Directional glyph per data flow; integrations with no known flow keep the
// generic chip icon rather than guessing a direction.
const DATA_FLOW_ICONS: Record<DataFlow, typeof MemoryIcon> = {
  in: InputIcon,
  out: OutputIcon,
  both: SyncAltIcon,
  internal: CycleIcon,
}

interface IntNodeData extends Record<string, unknown> {
  name: string
  template: string
  dataFlow?: DataFlow
  selected: boolean
  /** Cron expression badge for schedule-triggered components (declared topology only). */
  trigger?: string
}

interface GroupNodeData extends Record<string, unknown> {
  title: string
  count: number
}

interface ExtNodeData extends Record<string, unknown> {
  name: string
  type: string
  direction?: DaprComponent['direction']
}

interface TopicNodeData extends Record<string, unknown> {
  name: string
  /** Data entity carried on the topic, e.g. "RawProduct". */
  entity?: string
  /** Contract ref when the topic is a declared API (contract surface). */
  contract?: string
}

// systemKey/systemLabel identify a system by its domain/system folder pair so
// two different domains that reuse a system name stay distinct. Integrations
// scaffolded at the workspace root fall under a "Workspace" pseudo-system.
function systemKey(it: IntegrationDetail): string {
  return [it.domain, it.system].filter(Boolean).join('/') || WORKSPACE_KEY
}

function systemLabel(it: IntegrationDetail): string {
  return [it.domain, it.system].filter(Boolean).join(' / ') || 'Workspace'
}

// topologyFor picks the declared topology covering a system's integrations:
// the record whose directory contains every integration in the group. A
// record at the workspace root ('.') contains everything.
function topologyFor(
  items: IntegrationDetail[],
  topologies: Topology[],
): Topology | undefined {
  if (items.length === 0) return undefined
  return topologies.find((t) =>
    items.every(
      (it) => t.path === '.' || it.path === t.path || it.path.startsWith(t.path + '/'),
    ),
  )
}

export function FlowView({ selected, onSelect, theme }: Props) {
  const [graph, setGraph] = useState<IntegrationDetail[] | null>(null)
  const [report, setReport] = useState<TopologyReport | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [system, setSystem] = useState<string | null>(null)
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])

  useEffect(() => {
    const fail = (e: unknown) => setError(e instanceof Error ? e.message : String(e))
    api.flow().then(setGraph).catch(fail)
    // The first topology fetch runs each host's graph verb (a dotnet build),
    // so this request can take a while — the loading state covers it.
    api.topologies().then(setReport).catch(fail)
  }, [])

  const refresh = () => {
    setRefreshing(true)
    api
      .refreshTopologies()
      .then(setReport)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setRefreshing(false))
  }

  const topologies = useMemo(() => report?.topologies ?? [], [report])

  // The dropdown lists every system present, with an integration count. When
  // a declared topology covers a group its system name is the authoritative
  // label, overriding the folder-derived one.
  const systems = useMemo<ComboOption[]>(() => {
    if (!graph) return []
    const groups = new Map<string, { opt: ComboOption; items: IntegrationDetail[] }>()
    for (const it of graph) {
      const key = systemKey(it)
      const g =
        groups.get(key) ?? { opt: { value: key, label: systemLabel(it), count: 0 }, items: [] }
      g.opt.count = (g.opt.count ?? 0) + 1
      g.items.push(it)
      groups.set(key, g)
    }
    for (const g of groups.values()) {
      const t = topologyFor(g.items, topologies)
      if (t) g.opt.label = t.system
    }
    return [...groups.values()]
      .map((g) => g.opt)
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [graph, topologies])

  // Default to the first system once data lands.
  useEffect(() => {
    if (system === null && systems.length > 0) setSystem(systems[0].value)
  }, [systems, system])

  const items = useMemo(
    () => (graph && system ? graph.filter((it) => systemKey(it) === system) : []),
    [graph, system],
  )

  // Only a declared topology is drawn — there is no inferred fallback. A
  // system whose host produced no record renders the empty state below.
  const declared = useMemo(() => topologyFor(items, topologies), [items, topologies])
  const built = useMemo(
    () => (declared ? buildDeclaredGraph(declared, items) : { nodes: [], edges: [] }),
    [declared, items],
  )
  useEffect(() => setNodes(built.nodes), [built, setNodes])

  // Reflect the shared selection without disturbing dragged positions.
  useEffect(() => {
    setNodes((ns) =>
      ns.map((n) =>
        n.type === 'integration'
          ? { ...n, data: { ...n.data, selected: n.id === selected } }
          : n,
      ),
    )
  }, [selected, setNodes])

  if (error) {
    return <div className="banner error">{error}</div>
  }
  if (!graph || report === null) {
    return <p className="empty">Loading… (building system hosts on first run)</p>
  }
  if (graph.length === 0) {
    return <p className="empty">No integrations to visualise.</p>
  }

  return (
    <div className="flow-view">
      <div className="flow-toolbar">
        <span className="flow-toolbar-label">System</span>
        <Combobox
          value={system}
          options={systems}
          onChange={setSystem}
          placeholder="Select a system…"
        />
        <button
          type="button"
          className="flow-refresh"
          onClick={refresh}
          disabled={refreshing}
          title="Re-run each system host's graph verb"
        >
          {refreshing ? 'Refreshing…' : 'Refresh topology'}
        </button>
      </div>
      {report.errors?.length ? (
        <div className="banner error">
          {report.errors.map((e) => (
            <div key={e}>{e}</div>
          ))}
        </div>
      ) : null}
      {declared ? (
        <div className="flow-canvas">
          <ReactFlowProvider>
            <FlowCanvas
              nodes={nodes}
              edges={built.edges}
              onNodesChange={onNodesChange}
              onSelect={onSelect}
              fitSignal={system ?? ''}
              theme={theme}
            />
          </ReactFlowProvider>
        </div>
      ) : (
        <p className="empty">
          No declared topology for this system. Make sure its system host
          supports the graph verb, then refresh.
        </p>
      )}
    </div>
  )
}

// FlowCanvas hosts the React Flow instance so it can refit the viewport once
// nodes are measured, and again whenever the selected system changes — the
// `fitView` prop alone fits at init while the async node list is still empty.
function FlowCanvas({
  nodes,
  edges,
  onNodesChange,
  onSelect,
  fitSignal,
  theme,
}: {
  nodes: Node[]
  edges: Edge[]
  onNodesChange: Parameters<typeof ReactFlow>[0]['onNodesChange']
  onSelect: (path: string) => void
  fitSignal: string
  theme: 'light' | 'dark'
}) {
  const rf = useReactFlow()
  const initialized = useNodesInitialized()

  useEffect(() => {
    if (initialized) rf.fitView({ padding: 0.2 })
  }, [initialized, fitSignal, rf])

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      nodeTypes={NODE_TYPES}
      colorMode={theme}
      minZoom={0.3}
      proOptions={{ hideAttribution: true }}
      onNodeClick={(_, node) => {
        if (node.type === 'integration') onSelect(node.id)
      }}
    >
      <Background gap={20} />
      <Controls showInteractive={false} />
      <MiniMap pannable zoomable />
    </ReactFlow>
  )
}


// refName extracts the display name from an entity ref: "component:pim-extractor"
// → "pim-extractor", "system:default/pim" → "pim".
function refName(ref: string): string {
  const rest = ref.slice(ref.indexOf(':') + 1)
  const slash = rest.lastIndexOf('/')
  return slash >= 0 ? rest.slice(slash + 1) : rest
}

// buildDeclaredGraph renders a system's declared topology (v1): components and
// the topics they publish/subscribe laid out left-to-right by flow depth
// inside the boundary, external systems (via connectors) outside it. Nothing
// here is inferred — every node and edge comes straight from the record's
// inline wiring.
function buildDeclaredGraph(
  topo: Topology,
  items: IntegrationDetail[],
): { nodes: Node[]; edges: Edge[] } {
  const comps = topo.components ?? []

  // Metadata lookups: topics carry the contract, connectors the transport and
  // external system a component's inline reference resolves against.
  const topicMeta = new Map(
    (topo.topics ?? []).map((t) => [`${t.pubsub}/${t.topic}`, t]),
  )
  const connMeta = new Map((topo.connectors ?? []).map((c) => [c.name, c]))

  // A topic node is identified by its (pubsub, topic) pair; a component by ref.
  const topicId = (pubsub: string, topic: string): string => `topic:${pubsub}/${topic}`

  // Map component refs to integration paths (a component's directory is its
  // name relative to the system root) so clicking a declared node selects the
  // scaffolded integration when one exists.
  const joinProject = (base: string, project: string): string => {
    if (project === '.' || project === '') return base
    return base === '.' ? project : `${base}/${project}`
  }
  const nodeId = new Map<string, string>()
  for (const c of comps) {
    const path = joinProject(topo.path, c.name)
    const matched = items.some((it) => it.path === path) ? path : undefined
    nodeId.set(`component:${c.name}`, matched ?? `component:${c.name}`)
  }

  // Collect the unique topics the components reference, and the internal flow
  // edges (component ⇄ topic) that decide layout depth.
  const topicRefs = new Map<string, { pubsub: string; topic: string }>()
  const flowEdges: Array<{ from: string; to: string }> = []
  for (const c of comps) {
    const cref = `component:${c.name}`
    for (const p of c.publishes ?? []) {
      const tid = topicId(p.pubsub, p.topic)
      topicRefs.set(tid, { pubsub: p.pubsub, topic: p.topic })
      flowEdges.push({ from: cref, to: tid })
    }
    for (const s of c.subscribes ?? []) {
      const tid = topicId(s.pubsub, s.topic)
      topicRefs.set(tid, { pubsub: s.pubsub, topic: s.topic })
      flowEdges.push({ from: tid, to: cref })
    }
  }
  for (const tid of topicRefs.keys()) nodeId.set(tid, tid)

  // Longest-path layering over the internal flow edges decides each node's
  // column. Kahn-style relaxation; anything left by a cycle lands in column 0
  // rather than failing the render.
  const internal = new Set<string>([
    ...comps.map((c) => `component:${c.name}`),
    ...topicRefs.keys(),
  ])
  const succ = new Map<string, string[]>()
  const indeg = new Map<string, number>()
  for (const r of internal) {
    succ.set(r, [])
    indeg.set(r, 0)
  }
  for (const e of flowEdges) {
    if (!internal.has(e.from) || !internal.has(e.to)) continue
    succ.get(e.from)?.push(e.to)
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1)
  }
  const depth = new Map<string, number>()
  const queue = [...internal].filter((r) => indeg.get(r) === 0)
  for (const r of queue) depth.set(r, 0)
  while (queue.length > 0) {
    const r = queue.shift()!
    for (const n of succ.get(r) ?? []) {
      depth.set(n, Math.max(depth.get(n) ?? 0, (depth.get(r) ?? 0) + 1))
      indeg.set(n, indeg.get(n)! - 1)
      if (indeg.get(n) === 0) queue.push(n)
    }
  }
  for (const r of internal) if (!depth.has(r)) depth.set(r, 0)

  const cols = Math.max(1, Math.max(...[...depth.values()], 0) + 1)
  const contentTop = GROUP_HEADER
  const groupWidth = GROUP_PAD * 2 + cols * NODE_W + (cols - 1) * COL_GAP
  const colX = (d: number) => GROUP_PAD + d * (NODE_W + COL_GAP)

  const nodes: Node[] = []
  const edges: Edge[] = []
  const groupId = 'system'
  const rowsInCol: number[] = new Array<number>(cols).fill(0)
  const place = (d: number): { x: number; y: number; row: number } => {
    const row = rowsInCol[d]++
    return { x: colX(d), y: contentTop + row * (NODE_H + ROW_GAP), row }
  }

  // Component positions, keyed by ref, so externals can anchor to the
  // component they touch instead of the far edge of the canvas.
  const compPos = new Map<string, { x: number; y: number; col: number }>()
  for (const c of comps) {
    const ref = `component:${c.name}`
    const col = depth.get(ref) ?? 0
    const pos = place(col)
    compPos.set(ref, { x: pos.x, y: pos.y, col })
    nodes.push({
      id: nodeId.get(ref)!,
      type: 'integration',
      parentId: groupId,
      extent: 'parent',
      position: { x: pos.x, y: pos.y },
      data: {
        name: c.name,
        template: c.kind,
        dataFlow: dataFlowFor(c.kind),
        selected: false,
      } satisfies IntNodeData,
    })
  }

  for (const [tid, ref] of topicRefs) {
    const pos = place(depth.get(tid) ?? 0)
    const meta = topicMeta.get(`${ref.pubsub}/${ref.topic}`)
    nodes.push({
      id: nodeId.get(tid)!,
      type: 'topic',
      parentId: groupId,
      extent: 'parent',
      // Topics are narrower than component cards; center them in the column.
      position: { x: pos.x + (NODE_W - INFRA_W) / 2, y: pos.y },
      style: { width: INFRA_W, height: INFRA_H },
      data: {
        name: ref.topic,
        contract: meta?.contract,
      } satisfies TopicNodeData,
    })
  }

  const maxRows = Math.max(1, ...rowsInCol)
  const groupHeight = contentTop + maxRows * (NODE_H + ROW_GAP) - ROW_GAP + GROUP_PAD
  nodes.unshift({
    id: groupId,
    type: 'group',
    position: { x: 0, y: 0 },
    data: { title: topo.system, count: comps.length } satisfies GroupNodeData,
    style: { width: groupWidth, height: groupHeight },
    draggable: false,
    selectable: false,
  })

  // Topic publish/subscribe edges. A publish leaves the component's right
  // ('ext') handle for the topic; a subscribe leaves the topic's right ('out')
  // handle for the subscriber.
  for (const c of comps) {
    const cref = `component:${c.name}`
    const comp = nodeId.get(cref)!
    for (const p of c.publishes ?? []) {
      const target = nodeId.get(topicId(p.pubsub, p.topic))
      if (!target) continue
      edges.push({
        id: `e:pub:${c.name}:${p.pubsub}/${p.topic}`,
        source: comp,
        sourceHandle: 'ext',
        target,
        targetHandle: 'in',
        markerEnd: { type: MarkerType.ArrowClosed },
        className: 'rf-edge-ext',
      })
    }
    for (const s of c.subscribes ?? []) {
      const source = nodeId.get(topicId(s.pubsub, s.topic))
      if (!source) continue
      edges.push({
        id: `e:sub:${s.pubsub}/${s.topic}:${c.name}`,
        source,
        sourceHandle: 'out',
        target: comp,
        targetHandle: 'in',
        markerEnd: { type: MarkerType.ArrowClosed },
        className: 'rf-edge-ext',
      })
    }
  }

  // Externals sit outside the boundary, anchored to the component they touch
  // so edges stay short: an input connector directly left of its component,
  // an output connector dropped below the component's column (stacked when
  // several share one). This avoids the whole-diagram crossings that a fixed
  // left/right screen edge produces in a multi-column pipeline.
  const belowCount = new Map<number, number>()
  const belowY = (col: number): number => {
    const n = belowCount.get(col) ?? 0
    belowCount.set(col, n + 1)
    return groupHeight + EXT_VGAP + n * (EXT_H + EXT_VGAP)
  }
  const pushExternal = (id: string, data: ExtNodeData, x: number, y: number): void => {
    if (nodes.some((n) => n.id === id)) return
    nodes.push({
      id,
      type: 'external',
      position: { x, y },
      style: { width: EXT_W, height: EXT_H },
      data,
      draggable: true,
    })
  }
  const centerUnder = (x: number) => x + (NODE_W - EXT_W) / 2

  for (const c of comps) {
    const cref = `component:${c.name}`
    const comp = nodeId.get(cref)!
    const anchor = compPos.get(cref)!
    for (const use of c.connectors ?? []) {
      const conn = connMeta.get(use.connector)
      const isInput = use.direction === 'in'
      const extId = `ext:${use.connector}`
      const data: ExtNodeData = {
        name: conn?.externalSystem ? refName(conn.externalSystem) : use.connector,
        type: conn?.transport.type ?? 'connector',
        direction: isInput ? 'input' : undefined,
      }
      if (isInput) {
        pushExternal(extId, data, anchor.x - EXT_W - EXT_GAP, anchor.y)
        edges.push({
          id: `e:conn:${c.name}:${use.connector}`,
          source: extId,
          sourceHandle: 'out',
          target: comp,
          targetHandle: 'in',
          markerEnd: { type: MarkerType.ArrowClosed },
          className: 'rf-edge-ext',
        })
      } else {
        pushExternal(extId, data, centerUnder(anchor.x), belowY(anchor.col))
        edges.push({
          id: `e:conn:${c.name}:${use.connector}`,
          source: comp,
          sourceHandle: 'infra',
          target: extId,
          targetHandle: 'top',
          markerEnd: { type: MarkerType.ArrowClosed },
          className: 'rf-edge-ext',
        })
      }
    }
  }

  return { nodes, edges }
}

function GroupNode({ data }: NodeProps) {
  const { title, count } = data as GroupNodeData
  return (
    <div className="rf-group">
      <div className="rf-group-title">
        <span>{title}</span>
        <span className="count">{count}</span>
      </div>
    </div>
  )
}

function IntegrationNode({ data }: NodeProps) {
  const { name, template, dataFlow, selected, trigger } = data as IntNodeData
  const Icon = dataFlow ? DATA_FLOW_ICONS[dataFlow] : MemoryIcon
  return (
    <div className={`rf-int${selected ? ' selected' : ''}`}>
      <div className="rf-int-head">
        <Icon className="rf-int-icon" aria-hidden />
        <span className="rf-int-name">{name}</span>
        {template && <span className="rf-int-tag">{template}</span>}
        {trigger && (
          <span className="rf-int-tag" title="schedule trigger">
            {trigger}
          </span>
        )}
      </div>

      <Handle type="target" position={Position.Left} id="in" />
      <Handle type="source" position={Position.Bottom} id="infra" />
      <Handle type="source" position={Position.Right} id="ext" />
    </div>
  )
}

// TopicNode is a declared pub/sub topic: the first-class hop between
// components a declared topology makes visible. Contract topics show their
// contract ref; internal ones their data entity.
function TopicNode({ data }: NodeProps) {
  const { name, entity, contract } = data as TopicNodeData
  // Topic names share a long common prefix, so lead with the short entity and
  // keep the full topic/contract in a tooltip. Fall back to the name when a
  // topic declares no entity.
  const primary = entity ?? name
  const secondary = contract ?? name
  const tip = contract ? `${name}\n${contract}` : name
  return (
    <div className="rf-infra pubsub" title={tip}>
      <Handle type="target" position={Position.Left} id="in" />
      <HubIcon className="rf-infra-icon" aria-hidden />
      <div className="rf-infra-text">
        <span className="rf-infra-name">{primary}</span>
        <span className="rf-infra-kind">{secondary}</span>
      </div>
      <Handle type="source" position={Position.Right} id="out" />
    </div>
  )
}

function ExternalNode({ data }: NodeProps) {
  const { name, type, direction } = data as ExtNodeData
  const isDisk = /localstorage|localfile|disk|file/i.test(type)
  const Icon = isDisk ? HardDriveIcon : CloudIcon
  const kind = direction === 'input' ? 'source' : isDisk ? 'disk' : 'external'
  return (
    <div className="rf-ext">
      <Handle type="target" position={Position.Left} id="in" />
      <Handle type="target" position={Position.Top} id="top" />
      <Handle type="source" position={Position.Right} id="out" />
      <Icon className="rf-ext-icon" aria-hidden />
      <div className="rf-ext-text">
        <span className="rf-ext-name">{name}</span>
        <span className="rf-ext-kind" title={type}>
          {kind}
        </span>
      </div>
    </div>
  )
}

const NODE_TYPES = {
  group: GroupNode,
  integration: IntegrationNode,
  external: ExternalNode,
  topic: TopicNode,
}
