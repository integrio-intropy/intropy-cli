import { useCallback, useEffect, useState } from 'react'
import {
  api,
  type CreateResponse,
  type TemplateDetail,
  type TemplateList,
  type TemplateSummary,
} from '../api'
import { FormField } from './form'
import { useTemplateFields } from './useTemplateFields'

/** The create placeholders on the flow canvas, by archetype column:
 *  inbound edge → core → outbound edge. */
export type SlotKind = 'edge-in' | 'core' | 'edge-out'

// The intropy.io/data-flow label values each slot offers templates for. The
// label is what places a block on the system boundary, so a new edge kind
// lands in its slot without a change here.
const KIND_DATA_FLOWS: Record<SlotKind, string[]> = {
  'edge-in': ['in'],
  core: ['both', 'internal'],
  'edge-out': ['out'],
}

/** The data-flow values a slot offers, for its empty-state message. */
export function slotDataFlows(kind: SlotKind): string[] {
  return KIND_DATA_FLOWS[kind]
}

/** The block templates a slot offers: those on its side of the system
 *  boundary. Support projects (system host, shared library) carry no block
 *  kind, and a slot scaffolds a block, so they never qualify. */
export function slotCandidates(list: TemplateList, kind: SlotKind): TemplateSummary[] {
  const want = KIND_DATA_FLOWS[kind]
  return templateEntries(list).filter(
    (e) =>
      e.labels?.['intropy.io/block-kind'] &&
      want.includes(e.labels?.['intropy.io/data-flow']?.toLowerCase() ?? ''),
  )
}

/** Every template in the listing, for a slot's "show all" escape hatch. */
export function templateEntries(list: TemplateList): TemplateSummary[] {
  return list.entries ?? list.templates.map((name) => ({ name }))
}

// optionLabel names a candidate with its block kind, so an edge slot's
// choice between extractor and gateway reads as one.
export function optionLabel(c: TemplateSummary): string {
  const base = c.title ? `${c.title} (${c.name})` : c.name
  const kind = c.labels?.['intropy.io/block-kind']
  return kind && kind !== c.name ? `${base} · ${kind}` : base
}

type CreateState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'error'; message: string; status: number }

interface Props {
  /** The template picked from a slot's menu. */
  template: TemplateSummary
  /** Root-relative directory the new block scaffolds into ("." = workspace root). */
  systemPath: string
  systemLabel: string
  onClose: () => void
  /** Called with the created scaffold's root-relative path — the caller
   *  refreshes /api/flow so the new block appears as a ghost node. */
  onCreated: (outputDir: string) => void
}

// CreateDrawer is the flow view's create surface: opened by picking a
// template from a placeholder slot's menu, it renders that template's
// manifest as the same form the templates view uses, scoped to the slot's
// system. Success is reported to the caller — the drawer closes and the
// canvas shows the new scaffold as a ghost node.
export function CreateDrawer({ template, systemPath, systemLabel, onClose, onCreated }: Props) {
  return (
    <aside className="create-drawer">
      <header className="create-drawer-head">
        <div>
          <h2 className="create-drawer-title">New {template.title ?? template.name}</h2>
          <span className="create-drawer-sys">in {systemLabel}</span>
        </div>
        <button type="button" className="flow-detail-close" onClick={onClose} aria-label="Close">
          ×
        </button>
      </header>
      <DrawerForm
        key={template.name}
        template={template.name}
        systemPath={systemPath}
        onCreated={onCreated}
      />
    </aside>
  )
}

// DrawerForm mirrors the templates view's TemplatePanel: manifest fetched,
// defaults seeded, required fields tracked, force offered on a 409. It is
// keyed by template name so switching templates resets all form state. The
// differences: the output directory is locked under the slot's system, and
// success closes the drawer instead of rendering a result card.
function DrawerForm({
  template,
  systemPath,
  onCreated,
}: {
  template: string
  systemPath: string
  onCreated: Props['onCreated']
}) {
  const [detail, setDetail] = useState<TemplateDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { values, setValue, visible, optional, missing } = useTemplateFields(
    detail?.fields ?? [],
    { template, dir: systemPath },
  )
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [force, setForce] = useState(false)
  const [create, setCreate] = useState<CreateState>({ phase: 'idle' })

  useEffect(() => {
    api
      .getTemplate(template, systemPath)
      .then(setDetail)
      .catch((e: unknown) => setError(errText(e)))
  }, [template, systemPath])

  const run = useCallback(() => {
    setCreate({ phase: 'running' })
    // No name: the resolved "name" parameter kebab-cases into the
    // directory, confined inside the slot's system directory server-side.
    api
      .createTemplate(template, { dir: systemPath, values, force })
      .then((result: CreateResponse) => onCreated(result.outputDir))
      .catch((e: unknown) => {
        const err = e as { message?: string; status?: number }
        setCreate({ phase: 'error', message: errText(e), status: err.status ?? 0 })
      })
  }, [template, systemPath, values, force, onCreated])

  if (error) {
    return <div className="banner error">{error}</div>
  }
  if (!detail) {
    return <div className="empty">loading {template}…</div>
  }

  const canRun = missing.length === 0 && create.phase !== 'running'
  const prefix = systemPath === '.' ? '' : `${systemPath}/`

  return (
    <form
      className="template-form"
      onSubmit={(e) => {
        e.preventDefault()
        if (canRun) run()
      }}
    >
      {detail.description && <p className="template-desc">{detail.description}</p>}

      {prefix && <p className="form-hint">scaffolds under {prefix}</p>}

      {visible.map((f) => (
        <FormField key={f.name} field={f} value={values[f.name]} onChange={setValue} />
      ))}

      {missing.length > 0 && (
        <div className="form-hint">missing required: {missing.join(', ')}</div>
      )}

      {optional.length > 0 && (
        <>
          <button
            type="button"
            className="advanced-toggle"
            onClick={() => setShowAdvanced((s) => !s)}
          >
            {showAdvanced ? '▾' : '▸'} advanced ({optional.length})
          </button>
          {showAdvanced &&
            optional.map((f) => (
              <FormField key={f.name} field={f} value={values[f.name]} onChange={setValue} />
            ))}
        </>
      )}

      {create.phase === 'error' && create.status === 409 && (
        <label className="force-row">
          <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
          render into the non-empty directory anyway
        </label>
      )}

      <div className="form-actions">
        <button type="submit" className="run-btn" disabled={!canRun}>
          {create.phase === 'running' ? 'scaffolding…' : 'Scaffold'}
        </button>
      </div>

      {create.phase === 'error' && <div className="banner error">{create.message}</div>}
    </form>
  )
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
