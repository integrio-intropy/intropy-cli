import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  api,
  type OtlpAnyValue,
  type OtlpKeyValue,
  type OtlpResourceSpans,
  type OtlpSpan,
  type SystemInfo,
  type TelemetryResource,
} from '../api'
import { Combobox } from './Combobox'

// Tracing renders the running system's distributed traces straight from the
// Aspire dashboard's telemetry API (proxied at /api/telemetry/{system}/…),
// so tracing data is readable here without opening the Aspire dashboard
// itself. The prerequisite travels with the data: nothing renders until a
// system host is running (the flow view's start button), because the Aspire
// dashboard — the OTLP collector the traces live in — is a child of that
// host.

/** One trace: its spans plus the summary fields the list row shows. */
interface TraceSummary {
  id: string
  /** Root span name — the operation the trace represents. */
  name: string
  /** Service names participating, in first-appearance order. */
  services: string[]
  startNano: number
  /** End-to-end duration in milliseconds. */
  durationMs: number
  spanCount: number
  hasError: boolean
}

interface SpanNode {
  span: OtlpSpan
  service: string
  children: SpanNode[]
  depth: number
}

// ---- OTLP helpers -------------------------------------------------------

function attrValue(v: OtlpAnyValue | undefined): string {
  if (!v) return ''
  if (v.stringValue !== undefined) return v.stringValue
  if (v.intValue !== undefined) return v.intValue
  if (v.doubleValue !== undefined) return String(v.doubleValue)
  if (v.boolValue !== undefined) return String(v.boolValue)
  if (v.arrayValue?.values) return v.arrayValue.values.map(attrValue).join(', ')
  if (v.kvlistValue?.values) {
    return v.kvlistValue.values.map((kv) => `${kv.key}=${attrValue(kv.value)}`).join(', ')
  }
  return ''
}

function attr(attrs: OtlpKeyValue[] | undefined, key: string): string {
  const hit = attrs?.find((kv) => kv.key === key)
  return hit ? attrValue(hit.value) : ''
}

const SERVICE_UNKNOWN = 'unknown'

function serviceName(rs: OtlpResourceSpans): string {
  return attr(rs.resource?.attributes, 'service.name') || SERVICE_UNKNOWN
}

function nano(ts: string | undefined): number {
  return ts ? Number(ts) : 0
}

function spanDurationMs(s: OtlpSpan): number {
  return (nano(s.endTimeUnixNano) - nano(s.startTimeUnixNano)) / 1e6
}

function spanHasError(s: OtlpSpan): boolean {
  return s.status?.code === 2
}

/** Flatten OTLP resourceSpans into (span, service) pairs. */
function flattenSpans(data: OtlpResourceSpans[] | undefined): { span: OtlpSpan; service: string }[] {
  const out: { span: OtlpSpan; service: string }[] = []
  for (const rs of data ?? []) {
    const service = serviceName(rs)
    for (const ss of rs.scopeSpans ?? []) {
      for (const span of ss.spans ?? []) out.push({ span, service })
    }
  }
  return out
}

/** Group flattened spans into trace summaries, newest first. */
function summarizeTraces(data: OtlpResourceSpans[] | undefined): TraceSummary[] {
  const byTrace = new Map<string, { span: OtlpSpan; service: string }[]>()
  for (const pair of flattenSpans(data)) {
    const id = pair.span.traceId ?? ''
    if (!id) continue
    const list = byTrace.get(id) ?? []
    list.push(pair)
    byTrace.set(id, list)
  }
  const traces: TraceSummary[] = []
  for (const [id, pairs] of byTrace) {
    const starts = pairs.map((p) => nano(p.span.startTimeUnixNano))
    const ends = pairs.map((p) => nano(p.span.endTimeUnixNano))
    const start = Math.min(...starts)
    const end = Math.max(...ends)
    const root = pairs.find((p) => !p.span.parentSpanId) ?? pairs[0]
    traces.push({
      id,
      name: root.span.name ?? id.slice(0, 12),
      services: [...new Set(pairs.map((p) => p.service))],
      startNano: start,
      durationMs: (end - start) / 1e6,
      spanCount: pairs.length,
      hasError: pairs.some((p) => spanHasError(p.span)),
    })
  }
  traces.sort((a, b) => b.startNano - a.startNano)
  return traces
}

/** Build the span tree for the waterfall: roots first, children nested by
 *  parentSpanId, siblings in start order. A parent the payload does not
 *  carry attaches the span as a root — better a flat row than a lost span. */
function buildSpanTree(pairs: { span: OtlpSpan; service: string }[]): SpanNode[] {
  const nodes = new Map<string, SpanNode>()
  for (const p of pairs) {
    nodes.set(p.span.spanId ?? '', { span: p.span, service: p.service, children: [], depth: 0 })
  }
  const roots: SpanNode[] = []
  for (const node of nodes.values()) {
    const parent = node.span.parentSpanId ? nodes.get(node.span.parentSpanId) : undefined
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  const sortRec = (list: SpanNode[]) => {
    list.sort(
      (a, b) => nano(a.span.startTimeUnixNano) - nano(b.span.startTimeUnixNano),
    )
    for (const n of list) sortRec(n.children)
  }
  sortRec(roots)
  return roots
}

function flattenTree(roots: SpanNode[], depth = 0): SpanNode[] {
  const out: SpanNode[] = []
  for (const n of roots) {
    out.push({ ...n, depth })
    out.push(...flattenTree(n.children, depth + 1))
  }
  return out
}

function fmtDuration(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 1) return `${ms.toFixed(1)} ms`
  return `${(ms * 1000).toFixed(0)} µs`
}

function fmtTime(startNano: number): string {
  return new Date(startNano / 1e6).toLocaleTimeString(undefined, { hour12: false }) +
    '.' +
    String(Math.floor((startNano / 1e6) % 1000)).padStart(3, '0')
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** 503 from the proxy means the prerequisite is missing, not that the
 *  request failed — the panel renders it as guidance, not a banner. */
function isUnavailable(e: unknown): boolean {
  return e instanceof Error && 'status' in e && (e as { status?: number }).status === 503
}

// ---- Component ----------------------------------------------------------

export function Tracing() {
  const [systems, setSystems] = useState<SystemInfo[] | null>(null)
  const [system, setSystem] = useState<string | null>(null)
  const [resources, setResources] = useState<TelemetryResource[] | null>(null)
  const [resource, setResource] = useState<string>('')
  const [search, setSearch] = useState('')
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [traces, setTraces] = useState<TraceSummary[] | null>(null)
  const [unavailable, setUnavailable] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedTrace, setSelectedTrace] = useState<string | null>(null)
  const [detailSpans, setDetailSpans] = useState<OtlpResourceSpans[] | null>(null)
  const [detailError, setDetailError] = useState<string | null>(null)
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    api
      .systems()
      .then((list) => {
        setSystems(list)
        if (list.length > 0) setSystem((cur) => cur ?? list[0].path)
      })
      .catch((e: unknown) => setError(errText(e)))
  }, [])

  const sysPath = system ?? '.'

  // Resources for the filter dropdown; unknown until the dashboard answers.
  useEffect(() => {
    if (!system) return
    setResources(null)
    setResource('')
    api
      .telemetryResources(sysPath)
      .then(setResources)
      .catch(() => setResources(null))
  }, [system])

  const loadTraces = useCallback(
    (q: { resource: string; search: string; hasError?: boolean }) => {
      if (!system) return
      api
        .telemetryTraces(sysPath, {
          resource: q.resource || undefined,
          search: q.search || undefined,
          hasError: q.hasError,
          limit: 100,
        })
        .then((resp) => {
          setTraces(summarizeTraces(resp.data?.resourceSpans))
          setUnavailable(null)
          setError(null)
        })
        .catch((e: unknown) => {
          if (isUnavailable(e)) {
            setUnavailable(errText(e))
            setTraces(null)
          } else {
            setError(errText(e))
          }
        })
    },
    [system, sysPath],
  )

  // Initial + filter-driven load. Search is debounced; the other filters
  // apply immediately.
  useEffect(() => {
    if (!system) return
    setUnavailable(null)
    setError(null)
    if (searchTimer.current) clearTimeout(searchTimer.current)
    searchTimer.current = setTimeout(
      () => loadTraces({ resource, search, hasError: errorsOnly || undefined }),
      search ? 300 : 0,
    )
    return () => {
      if (searchTimer.current) clearTimeout(searchTimer.current)
    }
  }, [system, resource, search, errorsOnly, loadTraces])

  const openTrace = (id: string) => {
    setSelectedTrace(id)
    setDetailSpans(null)
    setDetailError(null)
    api
      .telemetryTrace(sysPath, id)
      .then((resp) => setDetailSpans(resp.data?.resourceSpans ?? []))
      .catch((e: unknown) => setDetailError(errText(e)))
  }

  const systemOptions = useMemo(
    () => (systems ?? []).map((s) => ({ value: s.path, label: s.name })),
    [systems],
  )

  if (systems && systems.length === 0) {
    return (
      <p className="empty">
        No systems in this workspace — tracing needs a running system host, and
        there is nothing to run yet.
      </p>
    )
  }

  return (
    <div className="tracing">
      <div className="tracing-toolbar">
        <span className="tracing-toolbar-label">System</span>
        <Combobox
          value={system ?? ''}
          options={systemOptions}
          onChange={setSystem}
          placeholder="Select a system…"
        />
        <span className="tracing-toolbar-label">Resource</span>
        <Combobox
          value={resource}
          options={(resources ?? []).map((r) => ({
            value: r.displayName ?? r.name,
            label: r.displayName ?? r.name,
          }))}
          onChange={setResource}
          placeholder="All resources"
        />
        <input
          type="search"
          className="tracing-search"
          placeholder="Search traces…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <label className="tracing-errors-only">
          <input
            type="checkbox"
            checked={errorsOnly}
            onChange={(e) => setErrorsOnly(e.target.checked)}
          />
          Errors only
        </label>
      </div>

      {error && <div className="banner error">{error}</div>}

      {unavailable ? (
        <div className="tracing-unavailable">
          <p>{unavailable}</p>
          <p className="tracing-hint">
            Traces are collected by the Aspire dashboard while the system host
            runs. Start the system from the Integration Flow view, then return
            here.
          </p>
        </div>
      ) : traces === null ? (
        <p className="empty">Loading traces…</p>
      ) : traces.length === 0 ? (
        <p className="empty">
          No traces yet — they appear once the running system handles requests.
        </p>
      ) : (
        <table className="tracing-list">
          <thead>
            <tr>
              <th>Name</th>
              <th>Time</th>
              <th>Duration</th>
              <th>Spans</th>
              <th>Services</th>
            </tr>
          </thead>
          <tbody>
            {traces.map((t) => (
              <tr
                key={t.id}
                className={`tracing-row${t.hasError ? ' has-error' : ''}${
                  selectedTrace === t.id ? ' selected' : ''
                }`}
                onClick={() => openTrace(t.id)}
              >
                <td className="tracing-name">{t.name}</td>
                <td>{fmtTime(t.startNano)}</td>
                <td>{fmtDuration(t.durationMs)}</td>
                <td>{t.spanCount}</td>
                <td className="tracing-services">{t.services.join(', ')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selectedTrace && (
        <TraceDetail
          traceId={selectedTrace}
          spans={detailSpans}
          error={detailError}
          onClose={() => setSelectedTrace(null)}
        />
      )}
    </div>
  )
}

// ---- Trace detail: waterfall + span panel -------------------------------

function TraceDetail({
  traceId,
  spans,
  error,
  onClose,
}: {
  traceId: string
  spans: OtlpResourceSpans[] | null
  error: string | null
  onClose: () => void
}) {
  const [selectedSpan, setSelectedSpan] = useState<SpanNode | null>(null)

  const rows = useMemo(() => {
    const pairs = flattenSpans(spans ?? undefined)
    return flattenTree(buildSpanTree(pairs))
  }, [spans])

  const bounds = useMemo(() => {
    if (rows.length === 0) return { start: 0, duration: 1 }
    const start = Math.min(...rows.map((r) => nano(r.span.startTimeUnixNano)))
    const end = Math.max(...rows.map((r) => nano(r.span.endTimeUnixNano)))
    return { start, duration: Math.max(end - start, 1) }
  }, [rows])

  return (
    <div className="trace-detail">
      <div className="trace-detail-header">
        <span className="trace-detail-title">Trace {traceId.slice(0, 16)}…</span>
        <button type="button" className="trace-detail-close" onClick={onClose}>
          Close
        </button>
      </div>
      {error && <div className="banner error">{error}</div>}
      {spans === null ? (
        <p className="empty">Loading trace…</p>
      ) : (
        <div className="trace-waterfall" role="list">
          {rows.map((row) => {
            const offset =
              ((nano(row.span.startTimeUnixNano) - bounds.start) / bounds.duration) * 100
            const width = Math.max(
              ((nano(row.span.endTimeUnixNano) - nano(row.span.startTimeUnixNano)) /
                bounds.duration) *
                100,
              0.5,
            )
            const isError = spanHasError(row.span)
            const isSelected =
              selectedSpan?.span.spanId === row.span.spanId &&
              selectedSpan?.span.traceId === row.span.traceId
            return (
              <button
                type="button"
                role="listitem"
                key={`${row.span.traceId}-${row.span.spanId}`}
                className={`trace-wf-row${isSelected ? ' selected' : ''}`}
                onClick={() => setSelectedSpan(isSelected ? null : row)}
              >
                <span
                  className="trace-wf-label"
                  style={{ paddingLeft: `${row.depth * 16}px` }}
                  title={`${row.service} · ${row.span.name ?? ''}`}
                >
                  <span className="trace-wf-service">{row.service}</span>
                  {row.span.name}
                </span>
                <span className="trace-wf-bar-track">
                  <span
                    className={`trace-wf-bar${isError ? ' error' : ''}`}
                    style={{ left: `${offset}%`, width: `${width}%` }}
                  />
                </span>
                <span className="trace-wf-duration">{fmtDuration(spanDurationMs(row.span))}</span>
              </button>
            )
          })}
        </div>
      )}
      {selectedSpan && <SpanPanel node={selectedSpan} />}
    </div>
  )
}

function SpanPanel({ node }: { node: SpanNode }) {
  const { span, service } = node
  return (
    <div className="span-panel">
      <div className="span-panel-title">
        {service} · {span.name}
      </div>
      <dl className="span-panel-facts">
        <dt>span id</dt>
        <dd>{span.spanId}</dd>
        <dt>duration</dt>
        <dd>{fmtDuration(spanDurationMs(span))}</dd>
        {span.status?.code === 2 && (
          <>
            <dt>status</dt>
            <dd className="span-error">error{span.status.message ? `: ${span.status.message}` : ''}</dd>
          </>
        )}
      </dl>
      {span.attributes && span.attributes.length > 0 && (
        <table className="span-attrs">
          <tbody>
            {span.attributes.map((kv) => (
              <tr key={kv.key}>
                <td>{kv.key}</td>
                <td>{attrValue(kv.value)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {span.events && span.events.length > 0 && (
        <div className="span-events">
          <div className="span-events-title">Events</div>
          {span.events.map((ev, i) => (
            <div key={i} className="span-event">
              <span className="span-event-name">{ev.name}</span>
              {ev.attributes?.map((kv) => (
                <div key={kv.key} className="span-event-attr">
                  {kv.key}={attrValue(kv.value)}
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
