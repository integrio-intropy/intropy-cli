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
  type SystemInfo,
  type Topology,
  type TopologyReport,
} from '../api'
import { CreateDrawer, type SlotKind } from './CreateDrawer'
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
import { FlowDetail, type FlowSelection } from './FlowDetail'

interface Props {
  selected: string | null
  onSelect: (path: string) => void
  /** Resolved app theme; drives React Flow's chrome (controls, minimap, edges). */
  theme: 'light' | 'dark'
}

// Layout geometry. The layout is deterministic: components and the topics
// they publish/subscribe flow left-to-right by declared depth inside the
// system boundary, and external systems (via ports) sit outside it.
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
  /** Scaffolded but not in the declared topology yet — rendered muted/dashed
   *  until `sys create` re-assembles the host and the topology is refreshed. */
  ghost?: boolean
}

interface SlotNodeData extends Record<string, unknown> {
  kind: SlotKind
}

interface GroupNodeData extends Record<string, unknown> {
  title: string
  count: number
}

interface ExtNodeData extends Record<string, unknown> {
  name: string
  type: string
  direction?: DaprComponent['direction']
  /** Declared port name — the inspector's join key to the topology's
   *  ports[] and messageDocs. */
  port?: string
}

interface TopicNodeData extends Record<string, unknown> {
  name: string
  /** Pub/sub component the topic lives on — with name, the inspector's join
   *  key into the topology's topics[]. */
  pubsub?: string
  /** Data entity carried on the topic, e.g. "RawProduct". */
  entity?: string
  /** Contract ref when the topic is a declared API (contract surface). */
  contract?: string
}

// systemKey identifies a system by its root directory, the one grouping key
// that stays unique when two directories declare the same system name (e.g. a
// copied system). Integrations scaffolded at the workspace root fall under a
// "Workspace" pseudo-system.
function systemKey(it: IntegrationDetail): string {
  return it.systemPath || WORKSPACE_KEY
}

function systemLabel(it: IntegrationDetail): string {
  return [it.domain, it.system].filter(Boolean).join(' / ') || 'Workspace'
}

// topologyFor picks the declared topology for a system: the record its host
// produced, which carries the system directory as its path. The Workspace
// pseudo-system maps to a record at the root ('.').
function topologyFor(key: string, topologies: Topology[]): Topology | undefined {
  const path = key === WORKSPACE_KEY ? '.' : key
  return topologies.find((t) => t.path === path)
}

export function FlowView({ selected, onSelect, theme }: Props) {
  const [graph, setGraph] = useState<IntegrationDetail[] | null>(null)
  const [systemList, setSystemList] = useState<SystemInfo[] | null>(null)
  const [report, setReport] = useState<TopologyReport | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [system, setSystem] = useState<string | null>(null)
  const [workspaceName, setWorkspaceName] = useState<string | null>(null)
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  // What the inspector panel shows: a clicked topic or port node.
  const [inspect, setInspect] = useState<FlowSelection | null>(null)
  // A clicked placeholder slot: which block kind the create drawer offers.
  const [draft, setDraft] = useState<SlotKind | null>(null)

  // A different system means different topics/ports — drop the inspector and
  // any half-open create drawer.
  useEffect(() => {
    setInspect(null)
    setDraft(null)
  }, [system])

  useEffect(() => {
    const fail = (e: unknown) => setError(e instanceof Error ? e.message : String(e))
    api.flow().then(setGraph).catch(fail)
    api.systems().then(setSystemList).catch(fail)
    // The workspace pseudo-system labels itself after the served folder;
    // a miss keeps the generic fallback rather than failing the view.
    api
      .health()
      .then((h) => setWorkspaceName(h.workspace || null))
      .catch(() => {})
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
  // label, overriding the folder-derived one. Systems that end up sharing a
  // label are qualified with their directory so the options stay tellable
  // apart — the underlying keys are always distinct.
  const systems = useMemo<ComboOption[]>(() => {
    if (!graph) return []
    const opts = new Map<string, ComboOption>()
    for (const it of graph) {
      const key = systemKey(it)
      const opt = opts.get(key) ?? { value: key, label: systemLabel(it), count: 0 }
      opt.count = (opt.count ?? 0) + 1
      opts.set(key, opt)
    }
    // Declared systems with no blocks yet (a host-only scaffold) still get an
    // option — they render as the empty slot skeleton, the create surface's
    // best onboarding moment.
    for (const s of systemList ?? []) {
      if (!opts.has(s.path)) opts.set(s.path, { value: s.path, label: s.name, count: 0 })
    }
    // A workspace with nothing in it at all still offers the workspace
    // pseudo-system, so the canvas opens as the fillable slot skeleton
    // instead of a dead end — the first block scaffolds at the root.
    if (opts.size === 0) {
      opts.set(WORKSPACE_KEY, { value: WORKSPACE_KEY, label: 'Workspace', count: 0 })
    }
    // The pseudo-system labels itself after the served folder; a topology
    // declared at the root (below) still overrides with its system name.
    const ws = opts.get(WORKSPACE_KEY)
    if (ws && workspaceName) ws.label = workspaceName
    for (const opt of opts.values()) {
      const t = topologyFor(opt.value, topologies)
      if (t) opt.label = t.system
    }
    const byLabel = new Map<string, ComboOption[]>()
    for (const opt of opts.values()) {
      byLabel.set(opt.label, [...(byLabel.get(opt.label) ?? []), opt])
    }
    for (const dup of byLabel.values()) {
      if (dup.length > 1) for (const opt of dup) opt.label = `${opt.label} (${opt.value})`
    }
    return [...opts.values()].sort((a, b) => a.label.localeCompare(b.label))
  }, [graph, systemList, topologies, workspaceName])

  // Default to the first system once data lands.
  useEffect(() => {
    if (system === null && systems.length > 0) setSystem(systems[0].value)
  }, [systems, system])

  const items = useMemo(
    () => (graph && system ? graph.filter((it) => systemKey(it) === system) : []),
    [graph, system],
  )

  // Only a declared topology contributes wiring — there is no inferred
  // fallback. A system without one still renders: a synthetic empty record
  // makes every scaffolded block a ghost and shows the slot skeleton, so a
  // brand-new system is a fillable shape instead of a dead end.
  const declared = useMemo(
    () => (system ? topologyFor(system, topologies) : undefined),
    [system, topologies],
  )
  const effective = useMemo<Topology | undefined>(() => {
    if (declared) return declared
    if (!system) return undefined
    const label = systems.find((o) => o.value === system)?.label ?? system
    return {
      path: system === WORKSPACE_KEY ? '.' : system,
      apiVersion: 'topology.intropy.io/v1',
      system: label,
    }
  }, [declared, system, systems])
  const built = useMemo(
    () =>
      effective
        ? buildDeclaredGraph(effective, items)
        : { nodes: [] as Node[], edges: [] as Edge[], ghosts: [] as IntegrationDetail[] },
    [effective, items],
  )
  useEffect(() => setNodes(built.nodes), [built, setNodes])

  // After a create, the new scaffold shows up as a ghost through the normal
  // join — the placeholder→ghost transition is a data refresh, not UI state.
  const handleCreated = () => {
    setDraft(null)
    api.flow().then(setGraph).catch((e: unknown) =>
      setError(e instanceof Error ? e.message : String(e)),
    )
  }

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
      {effective && !declared && (
        <div className="banner hint">
          No declared topology for this system yet — its components render
          from their scaffold records until the host's graph verb declares
          them.
        </div>
      )}
      {built.ghosts.length > 0 && (
        <div className="banner hint">
          {built.ghosts.length === 1
            ? '1 scaffolded component is'
            : `${built.ghosts.length} scaffolded components are`}{' '}
          not in the declared topology yet. Run <code>intropy sys create</code>{' '}
          to re-assemble the system host, then refresh the topology.
        </div>
      )}
      {effective ? (
        <div className="flow-canvas">
          <ReactFlowProvider>
            <FlowCanvas
              nodes={nodes}
              edges={built.edges}
              onNodesChange={onNodesChange}
              onSelect={onSelect}
              onInspect={(sel) => {
                setInspect(sel)
                if (sel) setDraft(null)
              }}
              onSlotClick={(kind) => {
                setDraft(kind)
                setInspect(null)
              }}
              fitSignal={system ?? ''}
              theme={theme}
            />
          </ReactFlowProvider>
          {inspect && declared && (
            <FlowDetail
              selection={inspect}
              topology={declared}
              onClose={() => setInspect(null)}
            />
          )}
          {draft && system && (
            <CreateDrawer
              kind={draft}
              systemPath={system === WORKSPACE_KEY ? '.' : system}
              systemLabel={systems.find((o) => o.value === system)?.label ?? system}
              onClose={() => setDraft(null)}
              onCreated={handleCreated}
            />
          )}
        </div>
      ) : (
        <p className="empty">Select a system.</p>
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
  onInspect,
  onSlotClick,
  fitSignal,
  theme,
}: {
  nodes: Node[]
  edges: Edge[]
  onNodesChange: Parameters<typeof ReactFlow>[0]['onNodesChange']
  onSelect: (path: string) => void
  onInspect: (selection: FlowSelection | null) => void
  onSlotClick: (kind: SlotKind) => void
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
        if (node.type === 'topic') {
          const d = node.data as TopicNodeData
          if (d.pubsub) onInspect({ kind: 'topic', pubsub: d.pubsub, topic: d.name })
        }
        if (node.type === 'external') {
          const d = node.data as ExtNodeData
          if (d.port) onInspect({ kind: 'port', name: d.port })
        }
        if (node.type === 'slot') {
          onSlotClick((node.data as SlotNodeData).kind)
        }
      }}
      onPaneClick={() => onInspect(null)}
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
// inside the boundary, external systems (via ports) outside it. Nothing
// here is inferred — every node and edge comes straight from the record's
// inline wiring.
function buildDeclaredGraph(
  topo: Topology,
  items: IntegrationDetail[],
): { nodes: Node[]; edges: Edge[]; ghosts: IntegrationDetail[] } {
  const comps = topo.components ?? []

  // Metadata lookups: topics carry the contract, ports the external
  // system a component's inline reference resolves against.
  const topicMeta = new Map(
    (topo.topics ?? []).map((t) => [`${t.pubsub}/${t.topic}`, t]),
  )
  const portMeta = new Map((topo.ports ?? []).map((c) => [c.name, c]))

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
  // At least three columns, so the create slots always sit in their archetype
  // positions (in → process → out) even when the declared graph is narrower —
  // an empty system renders as the fillable three-slot skeleton.
  const effCols = Math.max(cols, 3)
  const extractorCol = 0
  const loaderCol = Math.max(2, cols - 1)
  const transactionalCol = Math.min(loaderCol - 1, Math.max(1, Math.round(loaderCol / 2)))
  const contentTop = GROUP_HEADER
  const groupWidth = GROUP_PAD * 2 + effCols * NODE_W + (effCols - 1) * COL_GAP
  const colX = (d: number) => GROUP_PAD + d * (NODE_W + COL_GAP)

  const nodes: Node[] = []
  const edges: Edge[] = []
  const groupId = 'system'
  const rowsInCol: number[] = new Array<number>(effCols).fill(0)
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
        pubsub: ref.pubsub,
        contract: meta?.contract,
      } satisfies TopicNodeData,
    })
  }

  // Scaffolded-but-undeclared components: blocks whose scaffold record lives
  // in this system but whose name the declared topology does not carry —
  // brand new (pre `sys create`) or renamed. They render as ghosts in their
  // block kind's column, wireless, until the host declares them.
  const declaredPaths = new Set(comps.map((c) => joinProject(topo.path, c.name)))
  const ghosts = items.filter((it) => !declaredPaths.has(it.path))
  for (const g of ghosts) {
    const flow = g.dataFlow ?? (g.blockKind ? dataFlowFor(g.blockKind) : undefined)
    const col = flow === 'in' ? extractorCol : flow === 'out' ? loaderCol : transactionalCol
    const pos = place(col)
    nodes.push({
      id: g.path,
      type: 'integration',
      parentId: groupId,
      extent: 'parent',
      position: { x: pos.x, y: pos.y },
      data: {
        name: g.name,
        template: g.blockKind || g.template,
        dataFlow: flow,
        selected: false,
        ghost: true,
      } satisfies IntNodeData,
    })
  }

  // Placeholder slots — one per kind column, always last in their column.
  // Clicking one opens the create drawer pre-filtered to that block kind;
  // the slots are affordances, never part of the declared graph.
  const slotDefs: Array<{ kind: SlotKind; col: number }> = [
    { kind: 'extractor', col: extractorCol },
    { kind: 'transactional', col: transactionalCol },
    { kind: 'loader', col: loaderCol },
  ]
  for (const s of slotDefs) {
    const pos = place(s.col)
    nodes.push({
      id: `slot:${s.kind}`,
      type: 'slot',
      parentId: groupId,
      extent: 'parent',
      position: { x: pos.x, y: pos.y },
      style: { width: NODE_W, height: NODE_H },
      draggable: false,
      selectable: false,
      data: { kind: s.kind } satisfies SlotNodeData,
    })
  }

  const maxRows = Math.max(1, ...rowsInCol)
  const groupHeight = contentTop + maxRows * (NODE_H + ROW_GAP) - ROW_GAP + GROUP_PAD
  nodes.unshift({
    id: groupId,
    type: 'group',
    position: { x: 0, y: 0 },
    data: { title: topo.system, count: comps.length + ghosts.length } satisfies GroupNodeData,
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

  // Externals sit outside the boundary: a source (input port) directly left
  // of its component, a destination (output port) on a rail off the
  // boundary's right edge, aligned with its component's row and bumped down
  // when rows collide. Sources flow in on the left, destinations out on the
  // right — the same in → process → out reading as the columns inside.
  const railX = groupWidth + EXT_GAP
  const railYs: number[] = []
  const railY = (want: number): number => {
    let y = want
    while (railYs.some((used) => Math.abs(used - y) < EXT_H + EXT_VGAP)) {
      y += EXT_H + EXT_VGAP
    }
    railYs.push(y)
    return y
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

  for (const c of comps) {
    const cref = `component:${c.name}`
    const comp = nodeId.get(cref)!
    const anchor = compPos.get(cref)!
    for (const use of c.ports ?? []) {
      const port = portMeta.get(use.port)
      const isInput = use.direction === 'in'
      const extId = `ext:${use.port}`
      const data: ExtNodeData = {
        name: port?.externalSystem ? refName(port.externalSystem) : use.port,
        type: 'port',
        direction: isInput ? 'input' : undefined,
        port: use.port,
      }
      if (isInput) {
        pushExternal(extId, data, anchor.x - EXT_W - EXT_GAP, anchor.y)
        edges.push({
          id: `e:port:${c.name}:${use.port}`,
          source: extId,
          sourceHandle: 'out',
          target: comp,
          targetHandle: 'in',
          markerEnd: { type: MarkerType.ArrowClosed },
          className: 'rf-edge-ext',
        })
      } else {
        pushExternal(extId, data, railX, railY(anchor.y))
        edges.push({
          id: `e:port:${c.name}:${use.port}`,
          source: comp,
          sourceHandle: 'ext',
          target: extId,
          targetHandle: 'in',
          markerEnd: { type: MarkerType.ArrowClosed },
          className: 'rf-edge-ext',
        })
      }
    }
  }

  return { nodes, edges, ghosts }
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
  const { name, template, dataFlow, selected, trigger, ghost } = data as IntNodeData
  const Icon = dataFlow ? DATA_FLOW_ICONS[dataFlow] : MemoryIcon
  return (
    <div className={`rf-int${selected ? ' selected' : ''}${ghost ? ' ghost' : ''}`}>
      <div className="rf-int-head">
        <Icon className="rf-int-icon" aria-hidden />
        <span className="rf-int-name">{name}</span>
        {template && <span className="rf-int-tag">{template}</span>}
        {trigger && (
          <span className="rf-int-tag" title="schedule trigger">
            {trigger}
          </span>
        )}
        {ghost && (
          <span
            className="rf-int-tag ghost-tag"
            title="scaffolded, not in the declared topology yet"
          >
            scaffolded
          </span>
        )}
      </div>

      <Handle type="target" position={Position.Left} id="in" />
      <Handle type="source" position={Position.Bottom} id="infra" />
      <Handle type="source" position={Position.Right} id="ext" />
    </div>
  )
}

// SlotNode is a create placeholder: one per block-kind column, low-contrast
// until hovered. Clicking it opens the create drawer for that kind. It has no
// handles — a slot carries no wiring.
const SLOT_LABELS: Record<SlotKind, string> = {
  extractor: 'Extractor',
  transactional: 'Core block',
  loader: 'Loader',
}

function SlotNode({ data }: NodeProps) {
  const { kind } = data as SlotNodeData
  return (
    <div className="rf-slot">
      <span className="rf-slot-plus" aria-hidden>
        +
      </span>
      <div className="rf-slot-text">
        <span className="rf-slot-name">{SLOT_LABELS[kind]}</span>
        <span className="rf-slot-hint">scaffold a new block</span>
      </div>
    </div>
  )
}

// TopicNode is a declared pub/sub topic: the first-class hop between
// components a declared topology makes visible. Contract topics show their
// contract ref; internal ones their data entity. The node draws as a
// horizontal cylinder — the message-channel glyph — so a topic reads as
// infrastructure between components, never as another component card.
function TopicNode({ data }: NodeProps) {
  const { name, entity, contract } = data as TopicNodeData
  // Topic names share a long common prefix, so lead with the short entity and
  // keep the full topic/contract in a tooltip. Fall back to the name when a
  // topic declares no entity.
  const primary = entity ?? name
  const secondary = contract ?? name
  const tip = contract ? `${name}\n${contract}` : name
  return (
    <div className="rf-infra pubsub rf-topic-cyl" title={tip}>
      {/* Silhouette: a rectangle with both ends bulging as elliptical arcs;
          the extra arc is the near cap's seam, making the left end read as
          the visible face of the cylinder. */}
      <svg className="rf-cyl" viewBox="0 0 200 76" aria-hidden focusable="false">
        <path className="rf-cyl-body" d="M 12 3 H 188 A 9 35 0 0 1 188 73 H 12 A 9 35 0 0 1 12 3 Z" />
        <path className="rf-cyl-seam" d="M 12 3 A 9 35 0 0 1 12 73" />
      </svg>
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
  slot: SlotNode,
}
