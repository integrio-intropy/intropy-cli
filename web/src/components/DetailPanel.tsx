import type { ComponentType, ReactNode, SVGProps } from 'react'
import type { IntegrationDetail } from '../api'
import {
  DescriptionIcon,
  ExtensionIcon,
  RouteIcon,
  TuneIcon,
} from '../icons'

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>


interface Props {
  detail: IntegrationDetail | null
  hasSelection: boolean
}

export function DetailPanel({ detail, hasSelection }: Props) {
  if (!hasSelection) {
    return <p className="empty">Select an integration to see its details.</p>
  }
  if (!detail) {
    return <p className="empty">Loading…</p>
  }

  const values = Object.entries(detail.values ?? {})

  return (
    <article className="detail">
      <h1 className="detail-title">{title(detail)}</h1>

      <div className="meta-grid">
        <Meta label="Template" value={detail.template || '—'} />
        <Meta label="Version" value={detail.version} />
        <Meta label="Source" value={`${detail.owner}/${detail.repo}`} />
        <Meta label="Path" value={detail.path} />
      </div>

      <Section title="Values" icon={TuneIcon}>
        {values.length === 0 ? (
          <p className="muted">No scaffold values.</p>
        ) : (
          <table className="kv">
            <tbody>
              {values.map(([k, v]) => (
                <tr key={k}>
                  <th>{k}</th>
                  <td>{String(v)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Section>

      {detail.pipelineSteps && detail.pipelineSteps.length > 0 && (
        <Section title="Pipeline steps" icon={RouteIcon}>
          <ol className="steps">
            {detail.pipelineSteps.map((s) => (
              <li key={s}>{stepName(s)}</li>
            ))}
          </ol>
        </Section>
      )}

      {detail.components && detail.components.length > 0 && (
        <Section title="Dapr components" icon={ExtensionIcon}>
          <ul className="chips">
            {detail.components.map((c) => (
              <li key={c.file} className="chip" title={c.type}>
                {c.name}
                <span className="chip-sub">{c.type || c.category}</span>
              </li>
            ))}
          </ul>
        </Section>
      )}

      {detail.readme && (
        <Section title={detail.readme.name} icon={DescriptionIcon}>
          <pre className="doc">{detail.readme.content}</pre>
        </Section>
      )}
    </article>
  )
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="meta">
      <span className="meta-label">{label}</span>
      <span className="meta-value">{value}</span>
    </div>
  )
}

function Section({
  title,
  icon: Icon,
  children,
}: {
  title: string
  icon?: IconComponent
  children: ReactNode
}) {
  return (
    <section className="section">
      <h2 className="section-title">
        {Icon && <Icon className="icon" aria-hidden />}
        {title}
      </h2>
      {children}
    </section>
  )
}

// title matches the sidebar: the integration is named after its folder,
// prefixed with the domain and system folders when they exist.
function title(detail: IntegrationDetail): string {
  const name = detail.name || detail.path
  return [detail.domain, detail.system, name].filter(Boolean).join(' / ')
}

// stepName turns a source filename like "OrderValidateStep.cs" into a readable
// "Order Validate".
function stepName(file: string): string {
  return file
    .replace(/\.[^.]+$/, '')
    .replace(/Step$/, '')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
}
