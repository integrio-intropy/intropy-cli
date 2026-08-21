import { useEffect, useMemo, useRef, useState } from 'react'
import { ExpandMoreIcon, SearchIcon } from '../icons'

export interface ComboOption {
  value: string
  label: string
  count?: number
}

interface Props {
  value: string | null
  options: ComboOption[]
  onChange: (value: string) => void
  placeholder?: string
}

// Combobox is a small searchable single-select. It intentionally avoids a
// dependency: a trigger button that opens a filterable list, closing on
// outside-click or Escape. Used by FlowView to pick which system to show.
export function Combobox({ value, options, onChange, placeholder }: Props) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const rootRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const current = options.find((o) => o.value === value) ?? null

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return options
    return options.filter((o) => o.label.toLowerCase().includes(q))
  }, [options, query])

  // Close on outside click.
  useEffect(() => {
    if (!open) return
    function onDoc(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  // Focus the filter input when the list opens.
  useEffect(() => {
    if (open) inputRef.current?.focus()
    else setQuery('')
  }, [open])

  function pick(v: string) {
    onChange(v)
    setOpen(false)
  }

  return (
    <div className="combo" ref={rootRef}>
      <button
        type="button"
        className="combo-trigger"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <span className={`combo-value${current ? '' : ' placeholder'}`}>
          {current ? current.label : (placeholder ?? 'Select…')}
        </span>
        <ExpandMoreIcon className="combo-caret" aria-hidden />
      </button>

      {open && (
        <div className="combo-pop">
          <div className="combo-search">
            <SearchIcon className="combo-search-icon" aria-hidden />
            <input
              ref={inputRef}
              className="combo-input"
              value={query}
              placeholder="Search systems…"
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setOpen(false)
                if (e.key === 'Enter' && filtered.length > 0) pick(filtered[0].value)
              }}
            />
          </div>
          <ul className="combo-list" role="listbox">
            {filtered.length === 0 ? (
              <li className="combo-empty">No matches</li>
            ) : (
              filtered.map((o) => (
                <li key={o.value}>
                  <button
                    type="button"
                    role="option"
                    aria-selected={o.value === value}
                    className={`combo-option${o.value === value ? ' active' : ''}`}
                    onClick={() => pick(o.value)}
                  >
                    <span className="combo-option-label">{o.label}</span>
                    {o.count != null && <span className="combo-option-count">{o.count}</span>}
                  </button>
                </li>
              ))
            )}
          </ul>
        </div>
      )}
    </div>
  )
}
