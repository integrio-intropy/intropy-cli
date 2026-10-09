import type { TemplateField } from '../api'

// The template-form controls shared by every create surface (the flow
// view's create drawer, the seed drawer): one schema parameter rendered as
// the matching input, and the label + control row it sits in.

// FormField renders one schema parameter as the matching input: a combobox
// for an enum, a checkbox for a boolean, a number input for integer/number,
// a text input otherwise. Pattern and required come straight from the schema.
export function FormField({
  field,
  value,
  onChange,
}: {
  field: TemplateField
  value: unknown
  onChange: (key: string, v: unknown) => void
}) {
  const set = (v: unknown) => onChange(field.name, v)

  if (field.type === 'array' && field.items && field.items.length > 0) {
    return <ListField field={field} value={value} onChange={set} />
  }

  let input: React.ReactNode
  if (field.type === 'boolean') {
    input = (
      <input
        type="checkbox"
        checked={Boolean(value)}
        onChange={(e) => set(e.target.checked)}
      />
    )
  } else if (field.enum && field.enum.length > 0) {
    input = (
      <select
        value={value === undefined ? '' : String(value)}
        onChange={(e) => set(e.target.value || undefined)}
      >
        <option value="">—</option>
        {field.enum.map((opt) => (
          <option key={String(opt)} value={String(opt)}>
            {String(opt)}
          </option>
        ))}
      </select>
    )
  } else if (field.suggestions && field.suggestions.length > 0) {
    // Workspace candidates ride a datalist: the input stays free text
    // (a scaffold may deliberately declare a new topic) while the known
    // values are one arrow-key away. A select would close the list; the
    // CLI prompt's pick-list-plus-freetext is the parity target.
    const listId = `suggestions-${field.name}`
    input = (
      <>
        <input
          type="text"
          value={value === undefined ? '' : String(value)}
          pattern={field.pattern || undefined}
          list={listId}
          onChange={(e) => set(e.target.value === '' ? undefined : e.target.value)}
        />
        <datalist id={listId}>
          {field.suggestions.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      </>
    )
  } else if (field.type === 'integer' || field.type === 'number') {
    input = (
      <input
        type="number"
        value={value === undefined ? '' : String(value)}
        step={field.type === 'integer' ? 1 : 'any'}
        onChange={(e) => set(e.target.value === '' ? undefined : Number(e.target.value))}
      />
    )
  } else {
    input = (
      <input
        type="text"
        value={value === undefined ? '' : String(value)}
        pattern={field.pattern || undefined}
        onChange={(e) => set(e.target.value === '' ? undefined : e.target.value)}
      />
    )
  }

  return (
    <Field
      label={field.name}
      title={field.title}
      description={field.description}
      required={field.required}
      type={field.type}
    >
      {input}
    </Field>
  )
}

// Field is the label + control row every parameter renders through, with the
// same required marker `template show` prints.
export function Field({
  label,
  title,
  description,
  required,
  type,
  children,
}: {
  label: string
  title?: string
  description?: string
  required?: boolean
  type?: string
  children: React.ReactNode
}) {
  return (
    <label className="form-field">
      <span className="form-label">
        {required && <span className="req" aria-hidden>*</span>}
        {label}
        {title && <span className="form-label-title"> — {title}</span>}
        {type && <span className="form-type">[{type}]</span>}
      </span>
      {children}
      {description && <span className="form-desc">{description}</span>}
    </label>
  )
}

export function isEmpty(v: unknown): boolean {
  return v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0)
}

/** isIncomplete reports whether a value leaves a required parameter unanswered:
 *  empty, or — for a list of objects — short of its minimum, or with an element
 *  missing a required field. */
export function isIncomplete(f: TemplateField, v: unknown): boolean {
  if (f.type !== 'array' || !f.items) return isEmpty(v)
  const list = Array.isArray(v) ? (v as Record<string, unknown>[]) : []
  if (list.length < Math.max(f.minItems ?? 0, f.required ? 1 : 0)) return true
  return list.some((el) => f.items!.some((it) => it.required && isEmpty(el[it.name])))
}

type Element = Record<string, unknown>

// ListField renders a list of objects — a loader's routes — as ordered rows:
// one control per element field, with move, remove and add. Order is part of
// the value (the sidecar evaluates routes in order), so rows are numbered and
// movable. An element field left empty is dropped from its element, as the
// CLI prompt omits it.
function ListField({
  field,
  value,
  onChange,
}: {
  field: TemplateField
  value: unknown
  onChange: (v: unknown) => void
}) {
  const items = field.items!
  // A required list starts with one row to fill in; the value stays unset
  // until the first edit.
  const rows: Element[] = Array.isArray(value) && value.length > 0
    ? (value as Element[])
    : field.required ? [{}] : []
  const max = field.maxItems ?? Infinity
  const min = field.minItems ?? (field.required ? 1 : 0)

  const commit = (next: Element[]) => onChange(next.length === 0 ? undefined : next)
  const edit = (i: number, key: string, v: unknown) =>
    commit(rows.map((row, n) => {
      if (n !== i) return row
      const el = { ...row }
      if (isEmpty(v)) delete el[key]
      else el[key] = v
      return el
    }))
  const move = (i: number, by: number) => {
    const next = [...rows]
    const [row] = next.splice(i, 1)
    next.splice(i + by, 0, row)
    commit(next)
  }

  return (
    <fieldset className="form-field form-list">
      <legend className="form-label">
        {field.required && <span className="req" aria-hidden>*</span>}
        {field.name}
        {field.title && <span className="form-label-title"> — {field.title}</span>}
        <span className="form-type">[list]</span>
      </legend>
      {field.description && <span className="form-desc">{field.description}</span>}
      <ol className="form-list-rows">
        {rows.map((row, i) => (
          <li key={i} className="form-list-row">
            <div className="form-list-row-head">
              <span className="form-list-index">{i + 1}</span>
              <span className="form-list-actions">
                <button type="button" onClick={() => move(i, -1)} disabled={i === 0}
                  aria-label={`Move ${i + 1} up`} title="Evaluated earlier">↑</button>
                <button type="button" onClick={() => move(i, 1)} disabled={i === rows.length - 1}
                  aria-label={`Move ${i + 1} down`} title="Evaluated later">↓</button>
                <button type="button" onClick={() => commit(rows.filter((_, n) => n !== i))}
                  disabled={rows.length <= min} aria-label={`Remove ${i + 1}`} title="Remove">×</button>
              </span>
            </div>
            {items.map((it) => (
              <ElementField key={it.name} item={it} id={`${field.name}-${i}-${it.name}`}
                value={row[it.name]} onChange={(v) => edit(i, it.name, v)} />
            ))}
          </li>
        ))}
      </ol>
      <button type="button" className="form-list-add" onClick={() => commit([...rows, {}])}
        disabled={rows.length >= max}>
        + add {field.title ? singular(field.title).toLowerCase() : 'item'}
      </button>
    </fieldset>
  )
}

// ElementField is one field of a list element. A field named "when" is a
// content filter: monospace, with an example in its placeholder.
function ElementField({
  item,
  id,
  value,
  onChange,
}: {
  item: TemplateField
  id: string
  value: unknown
  onChange: (v: unknown) => void
}) {
  const text = value === undefined ? '' : String(value)
  const filter = item.name === 'when'
  const listId = `${id}-suggestions`
  return (
    <label className="form-field form-list-field">
      <span className="form-label">
        {item.required && <span className="req" aria-hidden>*</span>}
        {item.title ?? item.name}
        {!item.required && <span className="form-type">optional</span>}
      </span>
      <input
        type="text"
        className={filter ? 'form-cel' : undefined}
        value={text}
        pattern={item.pattern || undefined}
        placeholder={filter ? "event.data.status == '0'" : undefined}
        spellCheck={filter ? false : undefined}
        list={item.suggestions && item.suggestions.length > 0 ? listId : undefined}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      />
      {item.suggestions && item.suggestions.length > 0 && (
        <datalist id={listId}>
          {item.suggestions.map((s) => <option key={s} value={s} />)}
        </datalist>
      )}
      {item.description && <span className="form-desc">{item.description}</span>}
    </label>
  )
}

/** singular strips a plural title's trailing s for the add button. */
function singular(title: string): string {
  return title.endsWith('s') ? title.slice(0, -1) : title
}

// isResolved reports whether a parameter needs no answer from the user:
// the schema supplies a default, or the workspace supplies exactly one
// candidate. Both mirror the CLI's resolution — defaults and
// single-candidate prefill apply without prompting — so the create forms
// can show only what a run would actually ask.
export function isResolved(f: TemplateField): boolean {
  return f.default !== undefined || f.suggestions?.length === 1
}

// resolvedValue is the value a bare run would use for a resolved field,
// with the workspace candidate the fresher fact of the two.
export function resolvedValue(f: TemplateField): unknown {
  if (f.suggestions?.length === 1) return f.suggestions[0]
  return f.default
}
