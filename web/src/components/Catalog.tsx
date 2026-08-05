import { useEffect, useState } from 'react'
import { api, type CatalogCheck, type CatalogEntry, type DeployState } from '../api'
import { Section } from './chrome'
import { CatalogEnvironments } from './CatalogEnvironments'
import { CategoryIcon, OpenInNewIcon } from '../icons'

export interface DeployProps {
  state: DeployState | null
  loading: boolean
  refreshing: boolean
  onRefresh: () => void
}

interface Props {
  path: string | null
  deploy: DeployProps
}

// How often to re-fetch a pending catalog entry, and for how long. The first
// topology computation runs a dotnet build per system host, so a single retry
// would routinely lose the race; polling every few seconds for up to three
// minutes covers a cold build without polling forever. After the cap the
// refresh is left to the user.
const PENDING_RETRY_MS = 5_000
const PENDING_RETRY_CAP = 36

/** The catalog view: what a component is (header), what needs attention
 *  (checks), and what every environment runs. Identity and contracts come from
 *  the system topology graph — the scaffold record is provenance only, because
 *  it can go stale. */
export function Catalog({ path, deploy }: Props) {
  const [entry, setEntry] = useState<CatalogEntry | null>(null)

  useEffect(() => {
    if (!path) {
      setEntry(null)
      return
    }
    setEntry(null)
    let current = true
    let timer: ReturnType<typeof setTimeout> | undefined
    let attempts = 0

    const load = () => {
      api
        .catalog(path)
        .then((e) => {
          if (!current) return
          setEntry(e)
          // A cold topology cache answers pending while the server computes
          // the graph in the background; keep re-fetching until it resolves
          // or the cap leaves the refresh to the user.
          if (e.topologyPending && attempts < PENDING_RETRY_CAP) {
            attempts++
            timer = setTimeout(load, PENDING_RETRY_MS)
          }
        })
        .catch(() => {
          if (current) setEntry(null)
        })
    }
    load()

    return () => {
      current = false
      if (timer) clearTimeout(timer)
    }
  }, [path])

  if (!path) {
    return <p className="empty">Select an integration to see its catalog entry.</p>
  }
  if (!entry) {
    return <p className="empty">Loading…</p>
  }

  const checks = collectChecks(entry, deploy.state)

  return (
    <article className="detail">
      <CatalogHeader entry={entry} />
      <CatalogChecks checks={checks} />
      <CatalogEnvironments
        state={deploy.state}
        loading={deploy.loading}
        refreshing={deploy.refreshing}
        onRefresh={deploy.onRefresh}
      />
    </article>
  )
}

/** Component identity and contracts. In non-matched states the header renders
 *  from the scaffold record — provenance, not fact — and says where contracts
 *  went. */
function CatalogHeader({ entry }: { entry: CatalogEntry }) {
  const matched = entry.graphStatus === 'matched'

  return (
    <header className="catalog-header">
      <h1 className="detail-title">
        {entry.component}
        {entry.kind && <span className="catalog-kind">{entry.kind}</span>}
        {entry.system && <span className="catalog-system">{entry.system}</span>}
      </h1>
      {!matched && <p className="muted catalog-provenance">identity from scaffold record</p>}

      {matched ? (
        <div className="catalog-contracts">
          <ContractGroup label="Publishes" edges={entry.publishes} />
          <ContractGroup label="Subscribes" edges={entry.subscribes} />
        </div>
      ) : (
        <p className="muted">{contractsUnknown(entry)}</p>
      )}

      {entry.repository && (
        <p className="catalog-repo">
          <a
            href={`https://github.com/${entry.repository}`}
            target="_blank"
            rel="noreferrer"
            title={`Scaffold source repository: ${entry.repository}`}
          >
            <OpenInNewIcon className="icon" aria-hidden />
            {entry.repository}
          </a>
        </p>
      )}
    </header>
  )
}

function ContractGroup({
  label,
  edges,
}: {
  label: string
  edges: CatalogEntry['publishes']
}) {
  return (
    <div className="catalog-contract-group">
      <span className="meta-label">{label}</span>
      {!edges || edges.length === 0 ? (
        <span className="muted">—</span>
      ) : (
        <ul className="chips">
          {edges.map((e) => (
            <li key={`${e.pubsub}/${e.topic}`} className="chip" title={`${e.pubsub}/${e.topic}`}>
              {e.topic}
              {e.contract && <span className="chip-sub">{e.contract}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Why contracts are unknown, specific to the join state. */
function contractsUnknown(entry: CatalogEntry): string {
  switch (entry.graphStatus) {
    case 'pending':
      return 'Computing system graph… contracts will appear here.'
    case 'no-topology':
      return 'No system graph declares this component, so its contracts are unknown.'
    case 'not-in-graph':
      return 'This component is absent from its system’s graph, so its contracts are unknown.'
    case 'topology-error':
      return 'The system’s graph could not be computed, so contracts are unknown.'
    default:
      return 'Contracts are unknown.'
  }
}

/** Severity-ordered findings between header and environments: the server's
 *  graph checks plus the deploy-derived tag-pin and unpinned warnings. Notes
 *  stay in the Environments section, rendered verbatim as served. */
function CatalogChecks({ checks }: { checks: CatalogCheck[] }) {
  if (checks.length === 0) return null
  return (
    <Section title="Checks" icon={CategoryIcon}>
      <ul className="catalog-checks">
        {checks.map((c) => (
          <li key={c.message} className={`catalog-check ${c.severity}`}>
            {c.message}
          </li>
        ))}
      </ul>
    </Section>
  )
}

/** collectChecks merges the graph checks with what the deploy payload says
 *  about pin hygiene, warns before infos. Pin wording follows the command's
 *  own sentences, derived from the structured pins — never parsed out of note
 *  strings. */
function collectChecks(entry: CatalogEntry, deploy: DeployState | null): CatalogCheck[] {
  const warns: CatalogCheck[] = []
  const infos: CatalogCheck[] = []
  for (const c of entry.checks ?? []) {
    ;(c.severity === 'warn' ? warns : infos).push(c)
  }
  for (const env of deploy?.status?.environments ?? []) {
    if (!env.onboarded) continue
    for (const pin of env.pins ?? []) {
      if (pin.digest) continue
      if (pin.tag) {
        warns.push({
          severity: 'warn',
          message: `${env.environment} pins ${pin.image} to the tag "${pin.tag}" rather than a digest — it can drift without a deployment`,
        })
      } else {
        warns.push({
          severity: 'warn',
          message: `${env.environment} pins nothing for ${pin.image}, so it has never been deployed there`,
        })
      }
    }
  }
  return [...warns, ...infos]
}
