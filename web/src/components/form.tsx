import type { TemplateField } from '../api'

// The template-form controls shared by every create surface (the templates
// view, the flow view's create drawer): one schema parameter rendered as the
// matching input, and the label + control row it sits in.

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
  return v === undefined || v === null || v === ''
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
