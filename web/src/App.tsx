import { useCallback, useEffect, useState } from 'react'
import { api, type DeployState, type Integration } from './api'
import { Sidebar } from './components/Sidebar'
import { Catalog } from './components/Catalog'
import { FlowView } from './components/FlowView'
import { DarkModeIcon, LightModeIcon, SystemThemeIcon } from './icons'

const SIDEBAR_COLLAPSED_KEY = 'intropy.sidebar.collapsed'
const THEME_KEY = 'intropy.theme'

type View = 'catalog' | 'flow'

const VIEWS: { id: View; label: string }[] = [
  { id: 'catalog', label: 'Integration Catalog' },
  { id: 'flow', label: 'Integration Flow' },
]

// Theme preference cycles light → dark → system. "system" follows the OS,
// resolved to a concrete light/dark value that drives both data-theme (CSS)
// and the flow canvas colorMode.
type ThemePref = 'light' | 'dark' | 'system'
const THEME_ORDER: ThemePref[] = ['light', 'dark', 'system']
const THEME_META: Record<ThemePref, { Icon: typeof LightModeIcon; label: string }> = {
  light: { Icon: LightModeIcon, label: 'Light' },
  dark: { Icon: DarkModeIcon, label: 'Dark' },
  system: { Icon: SystemThemeIcon, label: 'System' },
}

function prefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function resolveTheme(pref: ThemePref): 'light' | 'dark' {
  return pref === 'system' ? (prefersDark() ? 'dark' : 'light') : pref
}

export default function App() {
  const [integrations, setIntegrations] = useState<Integration[] | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [deployState, setDeployState] = useState<DeployState | null>(null)
  const [deployLoading, setDeployLoading] = useState(false)
  const [deployRefreshing, setDeployRefreshing] = useState(false)
  const [version, setVersion] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [collapsed, setCollapsed] = useState<boolean>(
    () => localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === 'true',
  )
  const [view, setView] = useState<View>('catalog')
  const [theme, setTheme] = useState<ThemePref>(
    () => (localStorage.getItem(THEME_KEY) as ThemePref | null) ?? 'system',
  )
  const [resolvedTheme, setResolvedTheme] = useState<'light' | 'dark'>(() =>
    resolveTheme(theme),
  )

  useEffect(() => {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(collapsed))
  }, [collapsed])

  // Apply the theme to <html> and track the resolved value. In system mode,
  // also follow live OS changes; explicit modes ignore them.
  useEffect(() => {
    localStorage.setItem(THEME_KEY, theme)
    const apply = () => {
      const resolved = resolveTheme(theme)
      document.documentElement.dataset.theme = resolved
      setResolvedTheme(resolved)
    }
    apply()
    if (theme !== 'system') return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [theme])

  const cycleTheme = () =>
    setTheme((t) => THEME_ORDER[(THEME_ORDER.indexOf(t) + 1) % THEME_ORDER.length])

  useEffect(() => {
    api
      .listIntegrations()
      .then(setIntegrations)
      .catch((e: unknown) => setError(errText(e)))
    api
      .health()
      .then((h) => setVersion(h.version))
      .catch(() => {})
  }, [])

  // Auto-select the first integration once the list loads.
  useEffect(() => {
    if (integrations && integrations.length > 0 && !selected) {
      setSelected(integrations[0].path)
    }
  }, [integrations, selected])

  // Deployment state loads on its own, because it costs its own thing: reading
  // it refreshes a GitOps checkout over the network, while the catalog entry
  // is local files plus a cached topology. Keeping them apart lets the page
  // render straight away.
  //
  // A failed lookup is not an error banner: the reason travels inside the state
  // and belongs in the Deployment section, next to the Refresh that retries it.
  useEffect(() => {
    if (!selected) {
      setDeployState(null)
      return
    }
    setDeployState(null)
    setDeployLoading(true)
    let current = true
    api
      .deployState(selected)
      .then((s) => {
        if (current) setDeployState(s)
      })
      .catch((e: unknown) => {
        if (current) setDeployState({ error: errText(e), readAt: new Date().toISOString() })
      })
      .finally(() => {
        if (current) setDeployLoading(false)
      })
    // Selecting another integration while this is in flight must not let the
    // slower answer land on the newer selection.
    return () => {
      current = false
    }
  }, [selected])

  const refreshDeploy = useCallback(() => {
    if (!selected) return
    setDeployRefreshing(true)
    api
      .refreshDeployState(selected)
      .then(setDeployState)
      .catch((e: unknown) =>
        setDeployState({ error: errText(e), readAt: new Date().toISOString() }),
      )
      .finally(() => setDeployRefreshing(false))
  }, [selected])

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" aria-hidden />
          <span className="brand-name">Intropy</span>
          <span className="brand-sub">dashboard</span>
        </div>
        <nav className="nav">
          {VIEWS.map((v) => (
            <button
              key={v.id}
              className={`nav-item${view === v.id ? ' active' : ''}`}
              onClick={() => setView(v.id)}
              aria-current={view === v.id ? 'page' : undefined}
            >
              {v.label}
            </button>
          ))}
        </nav>
        <div className="topbar-end">
          {version && <span className="version">{version}</span>}
          <ThemeToggle theme={theme} onCycle={cycleTheme} />
        </div>
      </header>

      <div className="layout">
        {view === 'catalog' && (
          <Sidebar
            integrations={integrations}
            selected={selected}
            onSelect={setSelected}
            collapsed={collapsed}
            onToggle={() => setCollapsed((c) => !c)}
          />
        )}
        <main className="content">
          {error && <div className="banner error">{error}</div>}
          {view === 'catalog' ? (
            <Catalog
              path={selected}
              deploy={{
                state: deployState,
                loading: deployLoading,
                refreshing: deployRefreshing,
                onRefresh: refreshDeploy,
              }}
            />
          ) : null}
          {view === 'flow' && (
            <FlowView selected={selected} onSelect={setSelected} theme={resolvedTheme} />
          )}
        </main>
      </div>
    </div>
  )
}

// ThemeToggle cycles the theme preference. The current mode's glyph and a
// title communicate both the active choice and that clicking advances it.
function ThemeToggle({ theme, onCycle }: { theme: ThemePref; onCycle: () => void }) {
  const { Icon, label } = THEME_META[theme]
  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={onCycle}
      title={`Theme: ${label} (click to change)`}
      aria-label={`Theme: ${label}. Click to change.`}
    >
      <Icon className="icon" aria-hidden />
    </button>
  )
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
