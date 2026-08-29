import { useCallback, useEffect, useState } from 'react'
import {
  api,
  type CreateResponse,
  type TemplateDetail,
  type TemplateList,
} from '../api'
import { Field, FormField } from './form'
import { useTemplateFields } from './useTemplateFields'

interface Props {
  /** Switch to the catalog with the newly created integration selected. */
  onCreated: (path: string) => void
}

type CreateState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'done'; result: CreateResponse }
  | { phase: 'error'; message: string; status: number }

// TemplatesView is the `template` + `int create` surface: a list of the
// library's templates on the left, the selected one's manifest rendered as a
// form on the right, and a Run button that renders it into the workspace.
export function TemplatesView({ onCreated }: Props) {
  const [list, setList] = useState<TemplateList | null>(null)
  const [listError, setListError] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)

  useEffect(() => {
    api
      .listTemplates()
      .then(setList)
      .catch((e: unknown) => setListError(errText(e)))
  }, [])

  // Auto-select the first template once the list loads.
  useEffect(() => {
    if (list && list.templates.length > 0 && !selected) {
      setSelected(list.templates[0])
    }
  }, [list, selected])

  if (listError) {
    return <div className="banner error">{listError}</div>
  }
  if (!list) {
    return <div className="empty">loading templates…</div>
  }
  if (list.templates.length === 0) {
    return (
      <div className="empty">
        no templates in {list.owner}/{list.repo}@{list.version}
      </div>
    )
  }

  return (
    <div className="templates-view">
      <aside className="templates-list">
        <div className="templates-list-head">
          <span className="sidebar-title">Templates</span>
          <span className="count">{list.templates.length}</span>
        </div>
        <div className="templates-lib">
          {list.owner}/{list.repo}@{list.version}
        </div>
        <ul>
          {list.templates.map((name) => (
            <li key={name}>
              <button
                className={`template-item${selected === name ? ' active' : ''}`}
                onClick={() => setSelected(name)}
                aria-current={selected === name ? 'page' : undefined}
              >
                {name}
              </button>
            </li>
          ))}
        </ul>
      </aside>
      <section className="templates-detail">
        {selected ? (
          <TemplatePanel key={selected} name={selected} onCreated={onCreated} />
        ) : (
          <div className="empty">select a template</div>
        )}
      </section>
    </div>
  )
}

// TemplatePanel renders one template's manifest as the create form. It is
// keyed by template name, so switching templates resets all form state.
function TemplatePanel({ name, onCreated }: { name: string; onCreated: Props['onCreated'] }) {
  const [detail, setDetail] = useState<TemplateDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  // The templates view scaffolds into the workspace root, so it derives
  // from the whole workspace — the same context `int create` run there
  // prompts with.
  const { values, setValue, visible, optional, missing } = useTemplateFields(
    detail?.fields ?? [],
    { template: name, dir: '.' },
  )
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [intName, setIntName] = useState('')
  const [force, setForce] = useState(false)
  const [create, setCreate] = useState<CreateState>({ phase: 'idle' })

  useEffect(() => {
    api
      .getTemplate(name, '.')
      .then(setDetail)
      .catch((e: unknown) => setError(errText(e)))
  }, [name])

  const run = useCallback(() => {
    setCreate({ phase: 'running' })
    // name is the output directory (the CLI's --out-dir); values carries the
    // schema parameters, including the template's own `name` when declared.
    api
      .createTemplate(name, { name: intName, values, force })
      .then((result) => setCreate({ phase: 'done', result }))
      .catch((e: unknown) => {
        const err = e as { message?: string; status?: number }
        setCreate({ phase: 'error', message: errText(e), status: err.status ?? 0 })
      })
  }, [name, intName, values, force])

  if (error) {
    return <div className="banner error">{error}</div>
  }
  if (!detail) {
    return <div className="empty">loading {name}…</div>
  }

  const canRun = intName.trim() !== '' && missing.length === 0 && create.phase !== 'running'

  return (
    <div className="template-panel">
      <header className="template-head">
        <h2 className="template-title">{detail.title || detail.template}</h2>
        {detail.title && <span className="template-name">{detail.template}</span>}
        <span className="template-ref">
          {detail.owner}/{detail.repo}@{detail.version}
        </span>
      </header>
      {detail.description && <p className="template-desc">{detail.description}</p>}
      {detail.tags && detail.tags.length > 0 && (
        <div className="template-tags">
          {detail.tags.map((t) => (
            <span className="template-tag" key={t}>
              {t}
            </span>
          ))}
        </div>
      )}

      <div className="section-title">Parameters</div>
      <form
        className="template-form"
        onSubmit={(e) => {
          e.preventDefault()
          if (canRun) run()
        }}
      >
        <Field
          label="out-dir"
          title="Output directory"
          description="Where the integration is scaffolded, under the workspace root"
          required
        >
          <input
            type="text"
            value={intName}
            onChange={(e) => setIntName(e.target.value)}
            placeholder="my-integration"
            autoFocus
          />
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
      </form>

      {create.phase === 'error' && (
        <div className="banner error">{create.message}</div>
      )}
      {create.phase === 'done' && (
        <div className="create-result">
          <div className="create-result-head">
            created <code>{create.result.outputDir}</code> from {create.result.owner}/
            {create.result.repo}@{create.result.version}
          </div>
          {create.result.dependencies && create.result.dependencies.length > 0 && (
            <ul className="create-deps">
              {create.result.dependencies.map((d) => (
                <li key={d.outputDir}>
                  {d.template} → {d.outputDir} <em>({d.action})</em>
                </li>
              ))}
            </ul>
          )}
          <button className="flow-refresh" onClick={() => onCreated(create.result.outputDir)}>
            view in catalog
          </button>
        </div>
      )}
    </div>
  )
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
