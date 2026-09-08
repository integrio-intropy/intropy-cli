import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  api,
  type TestDataLibrary,
  type Topology,
} from '../api'
import { Combobox } from './Combobox'
import { Field } from './form'

/** What the seed drawer acts on: one component of one system, whose in-ports
 *  the declared topology carries. */
export interface SeedTarget {
  component: string
}

type SeedState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'done'; path: string }
  | { phase: 'error'; message: string; status: number }

interface Props {
  target: SeedTarget
  /** The system's declared topology — the drawer's source of in-ports and
   *  dev-folder resolutions. */
  topology: Topology
  onClose: () => void
  /** Re-runs the hosts' graph verbs (the flow toolbar's refresh); offered
   *  inline when a port has no dev folder, so a just-edited Development.cs is
   *  one click away. */
  onRefreshTopology: () => Promise<void> | void
}

// SeedDrawer is the flow view's seed surface: opened from an extractor node's
// action button, it copies one file from the system's test-file library
// (testdata/<port>/) into the dev inbox the host's development definition
// configures for the port. The interaction mirrors the create drawer: a
// combobox per choice, a 409 offering force, success closing the drawer.
export function SeedDrawer({ target, topology, onClose, onRefreshTopology }: Props) {
  // The component's external inputs, straight from the declared wiring.
  const inPorts = useMemo(() => {
    const comp = topology.components?.find((c) => c.name === target.component)
    return (comp?.ports ?? []).filter((p) => p.direction === 'in').map((p) => p.port)
  }, [topology, target.component])

  const [port, setPort] = useState<string | null>(inPorts[0] ?? null)
  const [file, setFile] = useState<string | null>(null)
  const [force, setForce] = useState(false)
  const [library, setLibrary] = useState<TestDataLibrary | null>(null)
  const [libError, setLibError] = useState<string | null>(null)
  const [seed, setSeed] = useState<SeedState>({ phase: 'idle' })
  const [refreshing, setRefreshing] = useState(false)
  // Late-open state: the drawer's component is known from the start, but the
  // system path arrives with the parent's topology prop (undefined while the
  // first graph run is in flight). The library fetch waits for it.
  const systemPath = topology.path

  useEffect(() => {
    if (!systemPath) return
    api
      .listTestData(systemPath)
      .then(setLibrary)
      .catch((e: unknown) => setLibError(errText(e)))
  }, [systemPath])

  const files = useMemo(() => (port ? (library?.ports[port] ?? []) : []), [library, port])

  // Keep the selections valid as the port/library data settles.
  useEffect(() => {
    setPort((cur) => (cur && inPorts.includes(cur) ? cur : (inPorts[0] ?? null)))
  }, [inPorts])
  useEffect(() => {
    setFile((cur) => (cur && files.includes(cur) ? cur : (files[0] ?? null)))
    setSeed({ phase: 'idle' })
    setForce(false)
  }, [files])

  const devFolder = useMemo(
    () => topology.development?.files?.find((f) => f.port === port)?.rootPath,
    [topology, port],
  )

  const run = useCallback(() => {
    if (!port || !file) return
    setSeed({ phase: 'running' })
    api
      .seedFile({
        systemPath,
        port,
        component: target.component,
        file,
        force,
      })
      .then((result) => setSeed({ phase: 'done', path: result.path }))
      .catch((e: unknown) => {
        const err = e as { message?: string; status?: number }
        setSeed({ phase: 'error', message: errText(e), status: err.status ?? 0 })
      })
  }, [systemPath, port, file, target.component, force])

  const refresh = useCallback(() => {
    setRefreshing(true)
    Promise.resolve(onRefreshTopology()).finally(() => setRefreshing(false))
  }, [onRefreshTopology])

  const canRun = port !== null && file !== null && seed.phase !== 'running'

  return (
    <aside className="create-drawer">
      <header className="create-drawer-head">
        <div>
          <h2 className="create-drawer-title">Seed test file</h2>
          <span className="create-drawer-sys">to {target.component}</span>
        </div>
        <button type="button" className="flow-detail-close" onClick={onClose} aria-label="Close">
          ×
        </button>
      </header>

      {inPorts.length === 0 && (
        <div className="empty">this component consumes no external ports</div>
      )}

      {inPorts.length > 0 && (
        <>
          <Field label="port" required>
            <Combobox
              value={port}
              options={inPorts.map((p) => ({ value: p, label: p }))}
              onChange={setPort}
              placeholder="Select a port…"
            />
          </Field>

          {port && devFolder === undefined && (
            <div className="banner hint">
              no dev folder configured for <code>{port}</code> — add{' '}
              <code>development.Files(Ports.{port}).RootPath(&quot;./test/{port}&quot;)</code> in
              the host&apos;s development definition, then refresh the topology. Hosts on
              Intropy.Topology older than v0.4.2 never emit one.
              <div>
                <button
                  type="button"
                  className="flow-refresh"
                  onClick={refresh}
                  disabled={refreshing}
                >
                  {refreshing ? 'Refreshing…' : 'Refresh topology'}
                </button>
              </div>
            </div>
          )}

          {port && devFolder !== undefined && libError && (
            <div className="banner error">{libError}</div>
          )}
          {port && devFolder !== undefined && library && files.length === 0 && (
            <div className="empty">
              no test files for <code>{port}</code> yet — drop sample payloads in{' '}
              <code>
                {systemPath === '.' ? '' : `${systemPath}/`}testdata/{port}/
              </code>{' '}
              (committed, sanitized)
            </div>
          )}

          {port && devFolder !== undefined && files.length > 0 && (
            <>
              <Field label="test file" required>
                <Combobox
                  value={file}
                  options={files.map((f) => ({ value: f, label: f }))}
                  onChange={setFile}
                  placeholder="Select a file…"
                />
              </Field>

              {seed.phase === 'error' && seed.status === 409 && (
                <label className="force-row">
                  <input
                    type="checkbox"
                    checked={force}
                    onChange={(e) => setForce(e.target.checked)}
                  />
                  replace the existing file in the dev folder
                </label>
              )}

              <div className="form-actions">
                <button
                  type="button"
                  className="run-btn"
                  disabled={!canRun}
                  onClick={run}
                >
                  {seed.phase === 'running' ? 'seeding…' : 'seed file'}
                </button>
              </div>

              {seed.phase === 'error' && <div className="banner error">{seed.message}</div>}
              {seed.phase === 'done' && (
                <div className="banner hint">
                  seeded <code>{seed.path}</code>
                </div>
              )}
            </>
          )}
        </>
      )}
    </aside>
  )
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
