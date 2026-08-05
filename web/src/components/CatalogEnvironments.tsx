import type { DeployState, EnvironmentStatus, StatusResult } from '../api'
import { Section } from './chrome'
import { CloudIcon } from '../icons'

interface Props {
  state: DeployState | null
  loading: boolean
  refreshing: boolean
  onRefresh: () => void
}

/** The environment ladder: desired (what the overlay pins) against live (what
 *  ArgoCD observes running), with the SemVer distance across every promotion
 *  edge. Everything comes from `intropy deploy status`; the sentences it
 *  computed are rendered as served and nothing is inferred from absence. */
export function CatalogEnvironments({ state, loading, refreshing, onRefresh }: Props) {
  if (loading && !state) {
    return (
      <Section title="Environments" icon={CloudIcon}>
        <p className="muted">Reading deployment state…</p>
      </Section>
    )
  }
  if (!state) return null

  return (
    <Section title="Environments" icon={CloudIcon}>
      {state.error ? (
        // The command's own message, unedited. It already names every way to
        // resolve the problem, and none of these is evidence about whether the
        // integration is deployed.
        <p className="deploy-error">{state.error}</p>
      ) : state.status ? (
        <Ladder status={state.status} diagnostics={state.diagnostics} />
      ) : (
        <p className="muted">No deployment state.</p>
      )}
      <Provenance state={state} refreshing={refreshing} onRefresh={onRefresh} />
    </Section>
  )
}

function Ladder({
  status,
  diagnostics,
}: {
  status: StatusResult
  diagnostics?: string[]
}) {
  const rows = status.environments ?? []
  const argocdRead = rows.some((e) => e.syncStatus || e.healthStatus)
  const releaseByEnv = new Map(rows.map((e) => [e.environment, e.release]))

  return (
    <>
      <p className={`deploy-verdict${status.consistent ? ' agrees' : ''}`}>{status.summary}</p>

      {!argocdRead && rows.length > 0 && (
        <div className="banner error">
          ArgoCD could not be read, so live state, sync and health are unknown —
          this says nothing about what the overlays pin.
          {diagnostics?.length ? (
            <ul className="deploy-diagnostics">
              {diagnostics.map((d) => (
                <li key={d}>{d}</li>
              ))}
            </ul>
          ) : null}
        </div>
      )}

      {rows.length === 0 ? (
        <p className="muted">This component declares no environments.</p>
      ) : (
        <table className="deploy-table">
          <thead>
            <tr>
              <th>Environment</th>
              <th>Desired</th>
              <th>Live</th>
              {argocdRead && <th>Sync</th>}
              <th>Age</th>
              <th>Promotion</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((env) => (
              <Row
                key={env.environment}
                env={env}
                argocdRead={argocdRead}
                sources={status.promotesFrom?.[env.environment] ?? []}
                releaseByEnv={releaseByEnv}
              />
            ))}
          </tbody>
        </table>
      )}

      {status.notes?.length ? (
        <ul className="deploy-notes">
          {status.notes.map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      ) : null}
    </>
  )
}

function Row({
  env,
  argocdRead,
  sources,
  releaseByEnv,
}: {
  env: EnvironmentStatus
  argocdRead: boolean
  sources: string[]
  releaseByEnv: Map<string, string | undefined>
}) {
  const span = argocdRead ? 6 : 5

  return (
    <tr className={env.onboarded ? undefined : 'not-onboarded'}>
      <th>
        {env.environment}
        {env.syncPolicy === 'manual' && <span className="deploy-tag">manual</span>}
        {env.pending && <span className="deploy-tag waiting">waiting</span>}
      </th>
      {env.onboarded ? (
        <>
          <td>
            <div className="catalog-desired">
              <span>{releaseCell(env)}</span>
              <Pins env={env} />
            </div>
          </td>
          <td>
            <Live env={env} />
          </td>
          {argocdRead && (
            <td>
              {env.syncStatus || NONE}
              {env.healthStatus ? ` · ${env.healthStatus}` : ''}
            </td>
          )}
          <td>{ageCell(env)}</td>
          <td>
            <PromotionDiff env={env} sources={sources} releaseByEnv={releaseByEnv} />
          </td>
        </>
      ) : (
        // Never omit the row: an environment the component declares but that
        // cannot be read is the fact an operator most needs to see.
        <td className="muted" colSpan={span}>
          {env.reason || 'no readable overlay here'}
        </td>
      )}
    </tr>
  )
}

/** Every declared image, not just the first. Digest pins shorten the same way
 *  the command shortens them; tag pins get the `:tag` treatment that signals
 *  drift risk. */
function Pins({ env }: { env: EnvironmentStatus }) {
  const pins = env.pins ?? []
  if (pins.length === 0) return <>{NONE}</>

  return (
    <ul className="deploy-pins">
      {pins.map((pin) => (
        <li key={pin.image}>
          <span className="deploy-image">{shortImage(pin.image)}</span>
          {pin.digest ? (
            <span title={pin.digest}>{shortDigest(pin.digest)}</span>
          ) : pin.tag ? (
            <span className="deploy-unpinned" title={`pinned to the tag ${pin.tag}, not a digest`}>
              :{pin.tag}
            </span>
          ) : (
            <span className="muted">{NONE}</span>
          )}
        </li>
      ))}
    </ul>
  )
}

/** What the cluster runs, from ArgoCD's summary. Agreement with the desired
 *  pins renders quietly; a mismatch renders both. Absent is unknown, and an
 *  empty cell is never a claim. */
function Live({ env }: { env: EnvironmentStatus }) {
  if (!env.liveImages) {
    return <span className="muted">unknown</span>
  }
  if (env.liveImages.length === 0) {
    return <span className="muted">{NONE}</span>
  }
  const desired = new Set((env.pins ?? []).filter((p) => p.digest).map((p) => p.digest))
  return (
    <ul className="deploy-pins">
      {env.liveImages.map((image) => {
        const digest = digestOf(image)
        const agrees = digest !== undefined && desired.has(digest)
        return (
          <li key={image}>
            <span title={image} className={agrees ? undefined : 'deploy-unpinned'}>
              {digest ? shortDigest(digest) : shortImage(image)}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

/** The SemVer distance to each environment this one promotes from, one line
 *  per source. A side that is not a release — commit-deployed or unpinned —
 *  makes no version claim at all. */
function PromotionDiff({
  env,
  sources,
  releaseByEnv,
}: {
  env: EnvironmentStatus
  sources: string[]
  releaseByEnv: Map<string, string | undefined>
}) {
  if (sources.length === 0) {
    return <span className="muted">{NONE}</span>
  }
  return (
    <ul className="catalog-promotion">
      {sources.map((source) => {
        const from = releaseByEnv.get(source)
        const d = diffReleases(from, env.release)
        return (
          <li key={source}>
            {d === null ? (
              <span className="muted">{source}: not a release</span>
            ) : d.relation === 'same' ? (
              <span className="muted">{source}: in sync ({env.release})</span>
            ) : (
              <span>
                {source} {from} → {env.release} · {d.magnitude} {d.relation}
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

interface ReleaseDiff {
  relation: 'same' | 'ahead' | 'behind'
  magnitude: 'patch' | 'minor' | 'major'
}

/** diffReleases mirrors the command's DiffReleases: strict X.Y.Z with an
 *  optional v prefix and prerelease, non-parse is not comparable rather than
 *  an error. Inputs are release annotation strings the command already
 *  recorded; anything else makes no claim. */
function diffReleases(from: string | undefined, to: string | undefined): ReleaseDiff | null {
  const f = from ? parseRelease(from) : undefined
  const t = to ? parseRelease(to) : undefined
  if (!f || !t) return null

  const cmp = compareRelease(f, t)
  if (cmp === 0) return { relation: 'same', magnitude: 'patch' }
  const magnitude = f[0] !== t[0] ? 'major' : f[1] !== t[1] ? 'minor' : 'patch'
  return { relation: cmp < 0 ? 'ahead' : 'behind', magnitude }
}

// [major, minor, patch, prerelease]. A release without a prerelease sorts
// after every prerelease of the same core — SemVer ordering.
type ParsedRelease = [number, number, number, string]

function parseRelease(v: string): ParsedRelease | undefined {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/.exec(v)
  if (!m) return undefined
  return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] ?? '']
}

function compareRelease(a: ParsedRelease, b: ParsedRelease): number {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1
  }
  if (a[3] === b[3]) return 0
  if (a[3] === '') return 1
  if (b[3] === '') return -1
  return a[3] < b[3] ? -1 : 1
}

function Provenance({ state, refreshing, onRefresh }: Omit<Props, 'loading'>) {
  return (
    <div className="deploy-foot">
      <div className="deploy-foot-text">
        {state?.readAt && <span className="muted">read {readAge(state.readAt)}</span>}
      </div>
      <button
        type="button"
        className="flow-refresh"
        onClick={onRefresh}
        disabled={refreshing}
        title="Re-run deploy status for this integration"
      >
        {refreshing ? 'Refreshing…' : 'Refresh'}
      </button>
    </div>
  )
}

/** What the terminal prints in an empty cell. */
const NONE = '—'

/** releaseCell mirrors the command: an environment deployed from a commit has
 *  no release but still has something true to say, and the `@` prefix is what
 *  keeps the commit from being misread as a version. */
function releaseCell(env: EnvironmentStatus): string {
  if (env.release) return env.release
  if (env.sourceCommit) return `@${env.sourceCommit.slice(0, 7)}`
  return NONE
}

function ageCell(env: EnvironmentStatus): string {
  return env.deployedAt ? humanAge(Date.now() - Date.parse(env.deployedAt)) : NONE
}

function readAge(readAt: string): string {
  const ms = Date.now() - Date.parse(readAt)
  return Number.isNaN(ms) ? 'just now' : `${humanAge(ms)} ago`
}

/** humanAge mirrors the command at one unit, truncating rather than rounding:
 *  119 minutes is "1h", never "2h". */
function humanAge(ms: number): string {
  const minutes = Math.floor(ms / 60_000)
  if (minutes < 1) return '<1m'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

/** shortDigest mirrors the command character for character, so a digest read
 *  here is the same string deploy and promote print. */
function shortDigest(digest: string): string {
  const prefix = 'sha256:'
  const hex = digest.startsWith(prefix) ? digest.slice(prefix.length) : null
  return hex && hex.length > 12 ? prefix + hex.slice(0, 12) : digest
}

/** shortImage drops the registry host so the column reads as a component
 *  name, keeping the last two path segments. */
function shortImage(image: string): string {
  const parts = image.split('/')
  return parts.length <= 2 ? image : parts.slice(-2).join('/')
}

/** digestOf extracts the pinned digest from a live image reference, which may
 *  carry a tag, a digest, or both. Live images match desired pins by exact
 *  digest — a suffix match would equate two digests sharing a prefix. */
function digestOf(image: string): string | undefined {
  const at = image.indexOf('@')
  return at >= 0 ? image.slice(at + 1) : undefined
}
