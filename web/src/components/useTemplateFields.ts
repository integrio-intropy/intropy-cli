import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type TemplateField } from '../api'
import { isEmpty, isResolved, resolvedValue } from './form'

// useTemplateFields partitions a template's parameters the way the CLI's
// resolution treats them:
//
//   visible  — parameters a run would ask for: required, with no default
//              and no single workspace candidate.
//   optional — everything resolved without asking (defaults and
//              single-candidate prefill), plus optional parameters, kept
//              behind the advanced toggle for explicit override.
//
// values seeds every resolved parameter with the value a bare run would
// use, so submitting without touching the form equals the CLI's
// no-prompt path, and editing an advanced field mirrors --set.
//
// With template+dir given, the hook also mirrors the CLI prompt loop's
// chained derivation: every confirmed answer feeds a suggestion refresh
// against the workspace (a picked topic narrows the contract candidates),
// so the fields re-partition as the form fills in. The server computes the
// candidates — the SPA never reimplements the convention rules.
export function useTemplateFields(
  fields: TemplateField[],
  suggestionScope?: { template: string; dir: string },
) {
  const [values, setValues] = useState<Record<string, unknown>>(() => seed(fields))
  // Live suggestion lists, keyed by parameter name. Starts as the detail
  // fetch's per-field lists; a suggestion refresh replaces it wholesale.
  const [liveSuggestions, setLiveSuggestions] = useState<Record<string, string[] | undefined>>(
    () => indexSuggestions(fields),
  )

  // Fields arrive asynchronously (the manifest fetch resolves after
  // mount), so seeding cannot stay a mount-time initializer. Merge new
  // seeds under existing values: a field the user already edited keeps
  // their input, everything else tracks the fetched manifest.
  const seededFor = useRef<TemplateField[]>(fields)
  useEffect(() => {
    if (seededFor.current === fields) return
    seededFor.current = fields
    setValues((prev) => ({ ...seed(fields), ...prev }))
    setLiveSuggestions(indexSuggestions(fields))
  }, [fields])

  // Suggestion refreshes race (one keystroke after another), so only the
  // latest request may land — a stale response would re-partition the form
  // against values the user has already moved past.
  const refreshSeq = useRef(0)
  useEffect(() => {
    if (!suggestionScope) return
    const seq = ++refreshSeq.current
    api
      .getTemplateSuggestions(suggestionScope.template, suggestionScope.dir, values)
      .then((r) => {
        if (seq === refreshSeq.current) setLiveSuggestions(r.suggestions)
      })
      .catch(() => {
        // A refresh failure keeps the current lists: the worst case is a
        // stale pick list, and the submit path re-validates regardless.
      })
    // The scope's members, not the object: forms rebuild it inline per
    // render, and dir/template changing is exactly a remount there.
  }, [suggestionScope?.template, suggestionScope?.dir, values])

  const live = useMemo(
    () =>
      fields.map((f) =>
        f.name in liveSuggestions ? { ...f, suggestions: liveSuggestions[f.name] } : f,
      ),
    [fields, liveSuggestions],
  )

  const visible = useMemo(
    () => live.filter((f) => f.required && !isResolved(f)),
    [live],
  )
  const optional = useMemo(
    () => live.filter((f) => !(f.required && !isResolved(f))),
    [live],
  )
  const missing = useMemo(
    () => visible.filter((f) => isEmpty(values[f.name])).map((f) => f.name),
    [visible, values],
  )
  const setValue = useCallback((key: string, v: unknown) => {
    setValues((prev) => ({ ...prev, [key]: v }))
  }, [])

  return { values, setValue, visible, optional, missing }
}

function seed(fields: TemplateField[]): Record<string, unknown> {
  const seeded: Record<string, unknown> = {}
  for (const f of fields) {
    if (isResolved(f)) seeded[f.name] = resolvedValue(f)
  }
  return seeded
}

function indexSuggestions(fields: TemplateField[]): Record<string, string[] | undefined> {
  const out: Record<string, string[] | undefined> = {}
  for (const f of fields) out[f.name] = f.suggestions
  return out
}
