import type { DeployState, EnvironmentStatus, StatusResult } from '../api'
import { Section } from './chrome'
import { CloudIcon } from '../icons'

interface Props {
  state: DeployState | null
  loading: boolean
  refreshing: boolean
  onRefresh: () => void
}

/** The environment ladder: the release and image pins each environment's
 *  overlay declares, plus sync/health when ArgoCD was readable. Everything
 *  comes from `intropy deploy status`; the sentences it computed are rendered
 *  as served and nothing is inferred from absence. */
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

  return (
    <>
      <Verdict status={status} />

      {!argocdRead && rows.length > 0 && (
        <div className="banner error">
          ArgoCD could not be read, so sync and health are unknown — this says
          nothing about what the overlays pin.
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
              <th>Version</th>
              <th>Image</th>
              {argocdRead && <th>Sync</th>}
              <th>Age</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((env, i) => (
              <Row key={env.environment} env={env} argocdRead={argocdRead} relation={relationToLast(rows, i)} />
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

/** The one-line answer under which the table sits, rendered only when there
 *  is one: agreement across every environment is the claim the command exists
 *  to make and is worth a sentence. A disagreement is positional — the
 *  markers on the rows already say which environment runs newer bits, and a
 *  sentence here would repeat them. */
function Verdict({ status }: { status: StatusResult }) {
  if (!status.consistent) return null
  return <p className="deploy-verdict agrees">{status.summary}</p>
}

/** Whether an environment runs the same bits as the furthest-downstream one:
 *  'same', 'ahead', 'behind', 'differs', or undefined when either side pins
 *  no digest (nothing to compare, never a disagreement claim). 'ahead' and
 *  'behind' are stated only when both sides carry a SemVer release to order
 *  by; a digest difference with no version to rank it is 'differs'. The
 *  furthest-downstream environment itself is always 'same'. */
function relationToLast(rows: EnvironmentStatus[], index: number): 'same' | 'ahead' | 'behind' | 'differs' | undefined {
  const self = rows[index]
  const last = [...rows].reverse().find((r) => r.onboarded && pinSignature(r))
  if (!last || last === self || !self.onboarded) return undefined
  const mine = pinSignature(self)
  if (!mine) return undefined
  if (mine === pinSignature(last)) return 'same'

  const mineV = self.release ? parseRelease(self.release) : undefined
  const lastV = last.release ? parseRelease(last.release) : undefined
  if (mineV && lastV) {
    const cmp = compareRelease(mineV, lastV)
    if (cmp < 0) return 'behind'
    if (cmp > 0) return 'ahead'
  }
  return 'differs'
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

/** pinSignature is an environment's digests in declared order, or undefined
 *  when any pin is missing — there is then no set of bits to compare. */
function pinSignature(env: EnvironmentStatus): string | undefined {
  const pins = env.pins ?? []
  if (pins.length === 0) return undefined
  const digests: string[] = []
  for (const p of pins) {
    if (!p.digest) return undefined
    digests.push(p.digest)
  }
  return digests.join(' ')
}

function Row({
  env,
  argocdRead,
  relation,
}: {
  env: EnvironmentStatus
  argocdRead: boolean
  relation?: 'same' | 'ahead' | 'behind' | 'differs'
}) {
  const span = argocdRead ? 4 : 3

  return (
    <tr className={env.onboarded ? undefined : 'not-onboarded'}>
      <th>
        {env.environment}
        {relation && relation !== 'same' && (
          <span
            className={`deploy-drift ${relation}`}
            title={
              relation === 'ahead'
                ? 'runs a newer release than the furthest-downstream environment — an unpromoted change'
                : relation === 'behind'
                  ? 'runs an older release than the furthest-downstream environment'
                  : 'runs different bits than the furthest-downstream environment'
            }
          >
            {relation === 'ahead' ? '↑ ahead' : relation === 'behind' ? '↓ behind' : '≠ differs'}
          </span>
        )}
        {env.syncPolicy === 'manual' && <span className="deploy-tag">manual</span>}
        {env.pending && <span className="deploy-tag waiting">waiting</span>}
      </th>
      {env.onboarded ? (
        <>
          <td>{releaseCell(env)}</td>
          <td>
            <Pins env={env} />
          </td>
          {argocdRead && (
            <td>
              {env.syncStatus || NONE}
              {env.healthStatus ? ` · ${env.healthStatus}` : ''}
            </td>
          )}
          <td>{ageCell(env)}</td>
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
