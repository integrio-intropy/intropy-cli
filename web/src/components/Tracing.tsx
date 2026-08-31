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
import { ChevronRightIcon, ExpandMoreIcon } from '../icons'

// Tracing renders the running system's distributed traces straight from the
// Aspire dashboard's telemetry API (proxied at /api/telemetry/{system}/…),
// so tracing data is readable here without opening the Aspire dashboard
// itself. The prerequisite travels with the data: nothing renders until a
// system host is running (the flow view's start button), because the Aspire
// dashboard — the OTLP collector the traces live in — is a child of that
// host.
//
// The list is the waterfall: each trace is one row, and expanding it nests
// its spans underneath on the same timeline, indented by depth. There is no
// separate detail view — a trace is its spans, so they share the row.

/** One trace row: the summary the collapsed row shows, plus the span rows
 *  an expanded row nests. Spans are fetched lazily on first expand. */
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

/** One span row of the waterfall: the span, its service, and its depth in
 *  the trace's parent/child tree (the row's indent). */
interface SpanRow {
  span: OtlpSpan
  service: string
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

/** Order a trace's spans as waterfall rows: depth-first over the
 *  parent/child tree, siblings in start order. A span whose parent the
 *  payload does not carry hangs at the top level — better a flat row than
 *  a lost span. */
function spanRows(data: OtlpResourceSpans[] | undefined): SpanRow[] {
  const pairs = flattenSpans(data)
  const byId = new Map<string, { span: OtlpSpan; service: string }>()
  for (const p of pairs) byId.set(p.span.spanId ?? '', p)
  const children = new Map<string, { span: OtlpSpan; service: string }[]>()
  const roots: { span: OtlpSpan; service: string }[] = []
  for (const p of pairs) {
    const parentId = p.span.parentSpanId ?? ''
    if (parentId && byId.has(parentId)) {
      const list = children.get(parentId) ?? []
      list.push(p)
      children.set(parentId, list)
    } else {
      roots.push(p)
    }
  }
  const byStart = (a: { span: OtlpSpan }, b: { span: OtlpSpan }) =>
    nano(a.span.startTimeUnixNano) - nano(b.span.startTimeUnixNano)
  const rows: SpanRow[] = []
  const walk = (list: { span: OtlpSpan; service: string }[], depth: number) => {
    list.sort(byStart)
    for (const p of list) {
      rows.push({ span: p.span, service: p.service, depth })
      walk(children.get(p.span.spanId ?? '') ?? [], depth + 1)
    }
  }
  walk(roots, 0)
  return rows
}

function fmtDuration(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 1) return `${ms.toFixed(1)} ms`
  return `${(ms * 1000).toFixed(0)} µs`
}

function fmtTime(startNano: number): string {
  return (
    new Date(startNano / 1e6).toLocaleTimeString(undefined, { hour12: false }) +
    '.' +
    String(Math.floor((startNano / 1e6) % 1000)).padStart(3, '0')
  )
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
  // Expanded traces: the waterfall rows per trace id, fetched on first
  // expand and reused after. A null entry means the fetch is in flight.
  const [expanded, setExpanded] = useState<Record<string, SpanRow[] | null>>({})
  const [expandErrors, setExpandErrors] = useState<Record<string, string>>({})
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
  }, [system, sysPath])

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

  const toggleTrace = (id: string) => {
    if (id in expanded) {
      setExpanded((prev) => {
        const next = { ...prev }
        delete next[id]
        return next
      })
      return
    }
    setExpanded((prev) => ({ ...prev, [id]: null }))
    api
      .telemetryTrace(sysPath, id)
      .then((resp) =>
        setExpanded((prev) => ({ ...prev, [id]: spanRows(resp.data?.resourceSpans) })),
      )
      .catch((e: unknown) => {
        setExpanded((prev) => {
          const next = { ...prev }
          delete next[id]
          return next
        })
        setExpandErrors((prev) => ({ ...prev, [id]: errText(e) }))
      })
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
        <div className="tracing-wf" role="tree" aria-label="Traces">
          {traces.map((t) => {
            const isOpen = t.id in expanded
            const rows = expanded[t.id]
            return (
              <div key={t.id} className="trace-group" role="treeitem" aria-expanded={isOpen}>
                <button
                  type="button"
                  className={`trace-row${t.hasError ? ' has-error' : ''}${isOpen ? ' open' : ''}`}
                  onClick={() => toggleTrace(t.id)}
                >
                  <span className="trace-caret" aria-hidden>
                    {isOpen ? <ExpandMoreIcon className="icon" /> : <ChevronRightIcon className="icon" />}
                  </span>
                  <span className="trace-name" title={t.name}>
                    {t.name}
                  </span>
                  <span className="trace-time">{fmtTime(t.startNano)}</span>
                  <span className="trace-spans">{t.spanCount} spans</span>
                  <span className="trace-services" title={t.services.join(', ')}>
                    {t.services.join(', ')}
                  </span>
                  <span className="trace-bar-track">
                    <span className={`trace-bar${t.hasError ? ' error' : ''}`} style={{ left: 0, width: '100%' }} />
                  </span>
                  <span className="trace-duration">{fmtDuration(t.durationMs)}</span>
                </button>
                {isOpen &&
                  (rows === null ? (
                    <p className="trace-loading">Loading spans…</p>
                  ) : (
                    <TraceSpans
                      rows={rows}
                      error={expandErrors[t.id]}
                    />
                  ))}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ---- One expanded trace's waterfall -------------------------------------

function TraceSpans({ rows, error }: { rows: SpanRow[]; error?: string }) {
  const [selected, setSelected] = useState<SpanRow | null>(null)

  const bounds = useMemo(() => {
    if (rows.length === 0) return { start: 0, duration: 1 }
    const start = Math.min(...rows.map((r) => nano(r.span.startTimeUnixNano)))
    const end = Math.max(...rows.map((r) => nano(r.span.endTimeUnixNano)))
    return { start, duration: Math.max(end - start, 1) }
  }, [rows])

  if (error) return <div className="banner error">{error}</div>
  if (rows.length === 0) return <p className="trace-loading">No spans in this trace.</p>

  return (
    <div className="trace-spans" role="group">
      {rows.map((row) => {
        const start = nano(row.span.startTimeUnixNano)
        const end = nano(row.span.endTimeUnixNano)
        const offset = ((start - bounds.start) / bounds.duration) * 100
        const width = Math.max(((end - start) / bounds.duration) * 100, 0.5)
        const isError = spanHasError(row.span)
        const isSelected =
          selected?.span.spanId === row.span.spanId && selected?.span.traceId === row.span.traceId
        return (
          <button
            type="button"
            key={`${row.span.traceId}-${row.span.spanId}`}
            className={`trace-row span-row${isError ? ' has-error' : ''}${isSelected ? ' selected' : ''}`}
            onClick={() => setSelected(isSelected ? null : row)}
          >
            <span className="trace-caret" aria-hidden />
            <span className="trace-name" style={{ paddingLeft: `${row.depth * 16}px` }} title={row.span.name}>
              <span className="trace-span-service">{row.service}</span>
              {row.span.name}
            </span>
            <span className="trace-bar-track">
              <span
                className={`trace-bar${isError ? ' error' : ''}`}
                style={{ left: `${offset}%`, width: `${width}%` }}
              />
            </span>
            <span className="trace-duration">{fmtDuration((end - start) / 1e6)}</span>
          </button>
        )
      })}
      {selected && <SpanFacts row={selected} />}
    </div>
  )
}

// The clicked span's facts, inline under the waterfall: identity, status,
// attributes, and events. It stays inline rather than a drawer because the
// waterfall above is the context the facts belong to.
function SpanFacts({ row }: { row: SpanRow }) {
  const { span, service } = row
  return (
    <div className="span-facts">
      <div className="span-facts-title">
        {service} · {span.name}
      </div>
      <dl className="span-facts-grid">
        <dt>span id</dt>
        <dd>{span.spanId}</dd>
        <dt>duration</dt>
        <dd>{fmtDuration((nano(span.endTimeUnixNano) - nano(span.startTimeUnixNano)) / 1e6)}</dd>
        {span.status?.code === 2 && (
          <>
            <dt>status</dt>
            <dd className="span-error">
              error{span.status.message ? `: ${span.status.message}` : ''}
            </dd>
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
