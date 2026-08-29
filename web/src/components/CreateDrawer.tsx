import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  api,
  type CreateResponse,
  type TemplateDetail,
  type TemplateList,
  type TemplateSummary,
} from '../api'
import { Combobox } from './Combobox'
import { Field, FormField } from './form'
import { useTemplateFields } from './useTemplateFields'

/** The three create placeholders on the flow canvas, one per archetype
 *  column: in → process → out. */
export type SlotKind = 'extractor' | 'transactional' | 'loader'

// The intropy.dev/block-kind label value each slot filters templates by.
const KIND_BLOCK_LABEL: Record<SlotKind, string> = {
  extractor: 'extractor',
  transactional: 'transactional-integration',
  loader: 'loader',
}

const KIND_TITLE: Record<SlotKind, string> = {
  extractor: 'extractor',
  transactional: 'core block',
  loader: 'loader',
}

type CreateState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'error'; message: string; status: number }

// norm compares label values the way dataFlowFor compares kinds: ignoring
// case and hyphens, so "transactional-integration" matches however the
// library spells it.
const norm = (s: string) => s.toLowerCase().replace(/-/g, '')

interface Props {
  kind: SlotKind
  /** Root-relative directory the new block scaffolds into ("." = workspace root). */
  systemPath: string
  systemLabel: string
  onClose: () => void
  /** Called with the created scaffold's root-relative path — the caller
   *  refreshes /api/flow so the new block appears as a ghost node. */
  onCreated: (outputDir: string) => void
}

// CreateDrawer is the flow view's create surface: opened by a placeholder
// slot, it offers the library's templates for that block kind and renders the
// selected one's manifest as the same form the templates view uses, scoped to
// the slot's system. Success is reported to the caller — the drawer closes
// and the canvas shows the new scaffold as a ghost node.
export function CreateDrawer({ kind, systemPath, systemLabel, onClose, onCreated }: Props) {
  const [list, setList] = useState<TemplateList | null>(null)
  const [listError, setListError] = useState<string | null>(null)
  const [showAll, setShowAll] = useState(false)
  const [tpl, setTpl] = useState<string | null>(null)

  useEffect(() => {
    api
      .listTemplates()
      .then(setList)
      .catch((e: unknown) => setListError(errText(e)))
  }, [])

  const candidates = useMemo<TemplateSummary[]>(() => {
    if (!list) return []
    const entries: TemplateSummary[] =
      list.entries ?? list.templates.map((name) => ({ name }))
    if (showAll) return entries
    const want = norm(KIND_BLOCK_LABEL[kind])
    return entries.filter((e) => norm(e.labels?.['intropy.dev/block-kind'] ?? '') === want)
  }, [list, showAll, kind])

  // Keep the selection valid as the candidate list settles (load, show-all).
  useEffect(() => {
    setTpl((cur) =>
      cur && candidates.some((c) => c.name === cur) ? cur : candidates[0]?.name ?? null,
    )
  }, [candidates])

  return (
    <aside className="create-drawer">
      <header className="create-drawer-head">
        <div>
          <h2 className="create-drawer-title">New {KIND_TITLE[kind]}</h2>
          <span className="create-drawer-sys">in {systemLabel}</span>
        </div>
        <button type="button" className="flow-detail-close" onClick={onClose} aria-label="Close">
          ×
        </button>
      </header>

      {listError && <div className="banner error">{listError}</div>}
      {!list && !listError && <div className="empty">loading templates…</div>}

      {list && candidates.length === 0 && (
        <div className="empty">
          no template in {list.owner}/{list.repo}@{list.version} carries{' '}
          <code>intropy.dev/block-kind: {KIND_BLOCK_LABEL[kind]}</code>
          <div>
            <button type="button" className="flow-refresh" onClick={() => setShowAll(true)}>
              show all templates
            </button>
          </div>
        </div>
      )}

      {list && candidates.length > 0 && (
        <>
          <Field label="template" required>
            <Combobox
              value={tpl}
              options={candidates.map((c) => ({
                value: c.name,
                label: c.title ? `${c.title} (${c.name})` : c.name,
              }))}
              onChange={setTpl}
              placeholder="Select a template…"
            />
          </Field>
          {!showAll && (
            <button
              type="button"
              className="drawer-show-all"
              onClick={() => setShowAll(true)}
            >
              show all templates
            </button>
          )}
          {tpl && (
            <DrawerForm
              key={tpl}
              template={tpl}
              systemPath={systemPath}
              onCreated={onCreated}
            />
          )}
        </>
      )}
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
  const [intName, setIntName] = useState('')
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
    api
      .createTemplate(template, { name: intName, dir: systemPath, values, force })
      .then((result: CreateResponse) => onCreated(result.outputDir))
      .catch((e: unknown) => {
        const err = e as { message?: string; status?: number }
        setCreate({ phase: 'error', message: errText(e), status: err.status ?? 0 })
      })
  }, [template, intName, systemPath, values, force, onCreated])

  if (error) {
    return <div className="banner error">{error}</div>
  }
  if (!detail) {
    return <div className="empty">loading {template}…</div>
  }

  const canRun = intName.trim() !== '' && missing.length === 0 && create.phase !== 'running'
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

      <Field
        label="out-dir"
        title="Output directory"
        description={
          prefix
            ? 'Scaffolded inside the system directory'
            : 'Scaffolded under the workspace root'
        }
        required
      >
        <div className="drawer-dir">
          {prefix && <span className="drawer-dir-prefix">{prefix}</span>}
          <input
            type="text"
            value={intName}
            onChange={(e) => setIntName(e.target.value)}
            placeholder="my-integration"
            autoFocus
          />
        </div>
      </Field>

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
          {create.phase === 'running' ? 'creating…' : 'int create'}
        </button>
      </div>

      {create.phase === 'error' && <div className="banner error">{create.message}</div>}
    </form>
  )
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
