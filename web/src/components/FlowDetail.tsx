import type { ReactNode } from 'react'
import type {
  Contract,
  JsonSchema,
  MessageDoc,
  Topology,
  TopologyPort,
  TopologyTopic,
} from '../api'
import {
  CategoryIcon,
  CloudIcon,
  DescriptionIcon,
  HubIcon,
  TuneIcon,
} from '../icons'
import { Meta, Section } from './chrome'

/** What the flow canvas has put under inspection: a pub/sub topic (internal
 *  message) or a port (external message). */
export type FlowSelection =
  | { kind: 'topic'; pubsub: string; topic: string }
  | { kind: 'port'; name: string }

interface Props {
  selection: FlowSelection
  topology: Topology
  onClose: () => void
}

/** FlowDetail is the flow view's inspector: what message flows here, and what
 *  does it look like. Topics render their contract's field tree (when the
 *  host published a schema); ports render the authored message doc from
 *  messages/<port>.md (when one exists). */
export function FlowDetail({ selection, topology, onClose }: Props) {
  return (
    <aside className="flow-detail">
      <div className="flow-detail-head">
        <span className="flow-detail-title">
          {selection.kind === 'topic' ? selection.topic : selection.name}
        </span>
        <button
          type="button"
          className="flow-detail-close"
          onClick={onClose}
          aria-label="Close details"
        >
          ×
        </button>
      </div>
      {selection.kind === 'topic' ? (
        <TopicDetail selection={selection} topology={topology} />
      ) : (
        <PortDetail name={selection.name} topology={topology} />
      )}
    </aside>
  )
}

function TopicDetail({
  selection,
  topology,
}: {
  selection: Extract<FlowSelection, { kind: 'topic' }>
  topology: Topology
}) {
  const topic: TopologyTopic | undefined = topology.topics?.find(
    (t) => t.pubsub === selection.pubsub && t.topic === selection.topic,
  )
  const contract: Contract | undefined = topic?.contract
    ? topology.contracts?.find((c) => c.name === topic.contract)
    : undefined

  return (
    <>
      <div className="meta-grid">
        <Meta label="Topic" value={selection.topic} />
        <Meta label="Pubsub" value={selection.pubsub} />
      </div>

      {(topic?.publishers?.length || topic?.subscribers?.length) ? (
        <Section title="Flow" icon={HubIcon}>
          <NameChips label="Publishers" names={topic?.publishers} />
          <NameChips label="Subscribers" names={topic?.subscribers} />
        </Section>
      ) : null}

      <Section title="Contract" icon={CategoryIcon}>
        {!topic?.contract ? (
          <p className="muted">This topic declares no contract.</p>
        ) : (
          <>
            <p className="flow-detail-contract" title={topic.contract}>
              {contract?.shortName ?? topic.contract}
            </p>
            {contract?.schema ? (
              <SchemaTree schema={contract.schema} />
            ) : (
              <p className="muted">
                No schema published by this host — rebuild it with an
                intropy-topology release that emits the contract registry to
                see the contract's fields here.
              </p>
            )}
            {contract?.fingerprint && (
              <p className="flow-detail-fingerprint" title="Schema fingerprint">
                {contract.fingerprint}
              </p>
            )}
          </>
        )}
      </Section>
    </>
  )
}

/** SchemaTree renders a contract's JSON Schema as the field tree a developer
 *  actually asks for: name, type, hierarchy, required. Constructs it does not
 *  recognize degrade to a plain type label — never an error. Exported for the
 *  catalog's contract detail, which renders the same tree. */
export function SchemaTree({ schema }: { schema: JsonSchema }) {
  const defs = schema.$defs ?? {}
  const rows: ReactNode[] = []

  const walk = (s: JsonSchema, depth: number, seen: Set<string>): void => {
    const target = resolve(s, defs, seen)
    if (!target?.properties) return
    const required = new Set(target.required ?? [])
    for (const [name, prop] of Object.entries(target.properties)) {
      rows.push(
        <tr key={`${depth}:${name}:${rows.length}`}>
          <td className="schema-name" style={{ paddingLeft: 8 + depth * 18 }}>
            {name}
          </td>
          <td className="schema-type">{typeLabel(prop, defs)}</td>
          <td className="schema-req">
            {required.has(name) && <span className="badge-required">required</span>}
          </td>
        </tr>,
      )
      const ref = refTarget(prop)
      if (ref && seen.has(ref)) continue
      walk(prop, depth + 1, ref ? new Set(seen).add(ref) : seen)
    }
  }
  walk(schema, 0, new Set())

  if (rows.length === 0) {
    return <p className="muted">The schema declares no fields.</p>
  }
  return (
    <table className="schema-tree">
      <tbody>{rows}</tbody>
    </table>
  )
}

// refTarget names the $def a schema (or the items of an array) points at, or
// undefined for inline shapes — used to cut recursive contracts short.
function refTarget(s: JsonSchema): string | undefined {
  if (s.$ref) return defName(s.$ref)
  if (s.type === 'array' && s.items) return refTarget(s.items)
  return undefined
}

// resolve follows a schema to the object whose properties should nest under
// it: through $ref into $defs and through array items.
function resolve(
  s: JsonSchema | undefined,
  defs: Record<string, JsonSchema>,
  seen: Set<string>,
): JsonSchema | undefined {
  if (!s) return undefined
  if (s.$ref) {
    const def = defs[defName(s.$ref)]
    return def === s ? undefined : def
  }
  if (s.type === 'array') return resolve(s.items, defs, seen)
  return s
}

function defName(ref: string): string {
  return ref.slice(ref.lastIndexOf('/') + 1)
}

// typeLabel is the human-facing type column: nullability folded in ("string"
// from ["string","null"]), arrays suffixed, refs by definition name, and
// anything unrecognized shown as-is rather than hidden.
function typeLabel(s: JsonSchema | undefined, defs: Record<string, JsonSchema>): string {
  if (!s) return 'unknown'
  if (s.$ref) return defName(s.$ref)
  if (s.enum) return 'enum'
  if (Array.isArray(s.type)) {
    const types = s.type.filter((t) => t !== 'null')
    const label = types.map((t) => labelFor(t, s, defs)).join(' | ')
    return label || 'null'
  }
  return labelFor(s.type, s, defs)
}

function labelFor(
  type: string | undefined,
  s: JsonSchema,
  defs: Record<string, JsonSchema>,
): string {
  if (type === 'array') return `${typeLabel(s.items, defs)}[]`
  if (type === 'string' && s.format) return s.format
  return type ?? 'unknown'
}

function PortDetail({ name, topology }: { name: string; topology: Topology }) {
  const port: TopologyPort | undefined = topology.ports?.find(
    (c) => c.name === name,
  )
  const doc: MessageDoc | undefined = topology.messageDocs?.[name]

  return (
    <>
      <div className="meta-grid">
        <Meta label="Port" value={name} />
        {port?.externalSystem && (
          <Meta label="External system" value={port.externalSystem} />
        )}
        {port?.directions?.length ? (
          <Meta label="Direction" value={port.directions.join(', ')} />
        ) : null}
      </div>

      <NameChips label="Used by" names={port?.usedBy} />

      {doc ? <MessageDocView doc={doc} /> : <MessageDocEmpty topology={topology} name={name} />}
    </>
  )
}

/** MessageDocView renders an authored payload description: the structured
 *  facts, the column table, the prose, and the (author-redacted) sample. */
function MessageDocView({ doc }: { doc: MessageDoc }) {
  const facts: Array<[string, string | undefined]> = [
    ['Format', doc.format],
    ['Delimiter', doc.delimiter],
    ['Encoding', doc.encoding],
    ['File pattern', doc.filePattern],
    ['Frequency', doc.frequency],
    ['Contact', doc.contact],
  ]
  const present = facts.filter((f): f is [string, string] => Boolean(f[1]))

  return (
    <>
      <Section title="Message" icon={TuneIcon}>
        {doc.lastReviewed && <ReviewedBadge date={doc.lastReviewed} />}
        {present.length > 0 ? (
          <table className="kv">
            <tbody>
              {present.map(([k, v]) => (
                <tr key={k}>
                  <th>{k}</th>
                  <td>{v}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="muted">No structured facts recorded.</p>
        )}
      </Section>

      {doc.fields && doc.fields.length > 0 && (
        <Section title="Fields" icon={CategoryIcon}>
          <table className="schema-tree">
            <tbody>
              {doc.fields.map((f, i) => (
                <tr key={`${f.name}:${i}`}>
                  <td className="schema-name">
                    {f.position ? `${f.position}. ` : ''}
                    {f.name}
                  </td>
                  <td className="schema-type">{f.type ?? ''}</td>
                  <td className="schema-req">
                    {f.required && <span className="badge-required">required</span>}
                  </td>
                  <td className="schema-notes">{f.notes ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Section>
      )}

      {doc.body && (
        <Section title="Notes" icon={DescriptionIcon}>
          <pre className="doc">{doc.body}</pre>
        </Section>
      )}

      {doc.sample?.inline && (
        <Section title="Sample" icon={CloudIcon}>
          <pre className="doc">{doc.sample.inline}</pre>
        </Section>
      )}
    </>
  )
}

/** The empty state doubles as the authoring onboarding: where to put the file
 *  and what to write in it. */
function MessageDocEmpty({ topology, name }: { topology: Topology; name: string }) {
  const dir = topology.path === '.' ? '' : `${topology.path}/`
  const today = new Date().toISOString().slice(0, 10)
  const template = `---
format: csv
delimiter: ";"
encoding: utf-8
filePattern: "*.csv"
frequency: describe when it arrives
fields:
  - name: FIELD_NAME
    type: string
    notes: what this column carries
sample:
  inline: "one;example;line"
  redacted: true
contact: who owns the source system
lastReviewed: ${today}
---
Free prose: where the data comes from, quirks, dedupe keys, gotchas.
`
  return (
    <Section title="Message" icon={DescriptionIcon}>
      <p className="muted">
        No message description yet. Describe what flows through this port
        in <code>{dir}messages/{name}.md</code>:
      </p>
      <pre className="doc">{template}</pre>
    </Section>
  )
}

// ReviewedBadge is the staleness story of an authored doc: it renders how
// long ago the author last vouched for it.
function ReviewedBadge({ date }: { date: string }) {
  const parsed = Date.parse(date)
  let ago = ''
  if (!Number.isNaN(parsed)) {
    const days = Math.floor((Date.now() - parsed) / 86_400_000)
    if (days >= 0) {
      if (days < 1) ago = 'today'
      else if (days < 61) ago = `${days} day${days === 1 ? '' : 's'} ago`
      else ago = `${Math.floor(days / 30.44)} months ago`
    }
  }
  return (
    <p className="flow-detail-reviewed" title={date}>
      reviewed {ago || date}
    </p>
  )
}

function NameChips({ label, names }: { label: string; names?: string[] }) {
  if (!names?.length) return null
  return (
    <div className="flow-detail-chips">
      <span className="meta-label">{label}</span>
      <ul className="chips">
        {names.map((n) => (
          <li key={n} className="chip">
            {n}
          </li>
        ))}
      </ul>
    </div>
  )
}
