import type { CatalogEntry, Contract, ContractEdge, JsonSchema } from '../api'
import { Section } from './chrome'
import { SchemaTree } from './FlowDetail'
import { CategoryIcon } from '../icons'

/** A placeholder schema, used only while the topology library does not emit a
 *  real one. Shaped like the Order contract so the detail reads as it will
 *  once the registry carries schemas; the banner above it says it is not real
 *  data. */
const PLACEHOLDER_SCHEMA: JsonSchema = {
  type: 'object',
  required: ['orderId', 'customerId', 'lines', 'placedAt'],
  properties: {
    orderId: { type: 'string', format: 'uuid' },
    customerId: { type: 'string' },
    lines: {
      type: 'array',
      items: {
        type: 'object',
        required: ['sku', 'quantity', 'unitPrice'],
        properties: {
          sku: { type: 'string' },
          quantity: { type: 'integer' },
          unitPrice: { type: 'number' },
        },
      },
    },
    placedAt: { type: 'string', format: 'date-time' },
    note: { type: 'string' },
  },
}

interface Props {
  /** The catalog entry the contract was clicked from — names this component. */
  entry: CatalogEntry
  /** The wire (topic) under inspection. */
  edge: ContractEdge
  /** The registry entry for the contract, when the topology emitted one. */
  contract?: Contract
  onClose: () => void
}

/** ContractDetail is the catalog's inspector for one contract: the wire it
 *  travels (which components publish and subscribe), and its field tree. The
 *  schema comes from the topology's contracts registry when the host emits
 *  one; until then a clearly-labelled placeholder shows what the section will
 *  look like. */
export function ContractDetail({ entry, edge, contract, onClose }: Props) {
  const schema = contract?.schema
  const name = edge.contract ?? edge.topic

  return (
    <aside className="flow-detail contract-detail">
      <div className="flow-detail-head">
        <span className="flow-detail-title">{name}</span>
        <button
          type="button"
          className="flow-detail-close"
          onClick={onClose}
          aria-label="Close details"
        >
          ×
        </button>
      </div>

      <Section title="Wire" icon={CategoryIcon}>
        <div className="meta-grid">
          <Wire label="Topic" value={`${edge.pubsub}/${edge.topic}`} />
          <Wire label="Published by" value={list(edge.publishers, entry.component)} />
          <Wire label="Subscribed by" value={list(edge.subscribers, entry.component)} />
        </div>
      </Section>

      <Section title="Schema" icon={CategoryIcon}>
        {schema ? (
          <SchemaTree schema={schema} />
        ) : (
          <>
            <p className="contract-placeholder-note">
              The system host does not publish a schema yet — this is placeholder
              data to show what the section will render once it does.
            </p>
            <SchemaTree schema={PLACEHOLDER_SCHEMA} />
          </>
        )}
      </Section>

      {contract?.fingerprint && (
        <p className="muted contract-fingerprint">fingerprint {contract.fingerprint}</p>
      )}
    </aside>
  )
}

function Wire({ label, value }: { label: string; value: string }) {
  return (
    <div className="meta">
      <span className="meta-label">{label}</span>
      <span className="meta-value">{value}</span>
    </div>
  )
}

/** list renders the components on one end of the wire, marking this one. The
 *  topology's publisher/subscriber lists are the source; the component the
 *  contract was clicked from is annotated rather than repeated blindly. */
function list(names: string[] | undefined, self: string): string {
  if (!names || names.length === 0) return '—'
  return names.map((n) => (n === self ? `${n} (this)` : n)).join(', ')
}
