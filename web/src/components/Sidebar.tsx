import { useState } from 'react'
import type { Integration } from '../api'
import {
  CategoryIcon,
  ChevronRightIcon,
  DomainIcon,
  LeftPanelCloseIcon,
  LeftPanelOpenIcon,
  MemoryIcon,
} from '../icons'

interface Props {
  integrations: Integration[] | null
  selected: string | null
  onSelect: (path: string) => void
  collapsed: boolean
  onToggle: () => void
}

// SystemGroup keys carry the domain prefix so equally named systems under
// different domains keep independent expand/collapse state.
interface SystemGroup {
  key: string
  system: string
  items: Integration[]
}

interface DomainGroup {
  domain: string
  systems: SystemGroup[]
  count: number
}

export function Sidebar({
  integrations,
  selected,
  onSelect,
  collapsed,
  onToggle,
}: Props) {
  const [closedGroups, setClosedGroups] = useState<Set<string>>(
    () => new Set(),
  )

  const toggleGroup = (key: string) =>
    setClosedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  const { domains, rootSystems, ungrouped } = groupTree(integrations ?? [])

  const systemGroup = (group: SystemGroup) => {
    const open = !closedGroups.has(group.key)
    return (
      <div className="sys-group" key={group.key}>
        <button
          className="sys-header"
          onClick={() => toggleGroup(group.key)}
          aria-expanded={open}
        >
          <ChevronRightIcon
            className={'icon caret' + (open ? ' open' : '')}
            aria-hidden
          />
          <CategoryIcon className="icon sys-icon" aria-hidden />
          <span className="sys-name">{group.system}</span>
          <span className="count">{group.items.length}</span>
        </button>
        {open && (
          <ul className="int-list nested">
            {group.items.map((it) => (
              <IntegrationItem
                key={it.path}
                integration={it}
                active={it.path === selected}
                onSelect={onSelect}
              />
            ))}
          </ul>
        )}
      </div>
    )
  }

  return (
    <aside className={'sidebar' + (collapsed ? ' collapsed' : '')}>
      <div className="sidebar-header">
        {!collapsed && (
          <span className="sidebar-title">
            Integrations
            {integrations && <span className="count">{integrations.length}</span>}
          </span>
        )}
        <button
          className="collapse-btn"
          onClick={onToggle}
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {collapsed ? (
            <LeftPanelOpenIcon className="icon" />
          ) : (
            <LeftPanelCloseIcon className="icon" />
          )}
        </button>
      </div>

      {!collapsed && integrations === null && (
        <p className="muted pad">Loading…</p>
      )}

      {!collapsed && integrations?.length === 0 && (
        <p className="muted pad">No scaffolded integrations found.</p>
      )}

      {collapsed ? (
        <ul className="int-list">
          {integrations?.map((it) => (
            <li key={it.path}>
              <button
                className={'int-item' + (it.path === selected ? ' active' : '')}
                onClick={() => onSelect(it.path)}
                title={`${it.name} — ${it.path}`}
              >
                <span className="int-initial">{initial(it.name)}</span>
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <nav className="tree">
          {domains.map((d) => {
            const key = 'domain:' + d.domain
            const open = !closedGroups.has(key)
            return (
              <div className="sys-group" key={key}>
                <button
                  className="sys-header"
                  onClick={() => toggleGroup(key)}
                  aria-expanded={open}
                >
                  <ChevronRightIcon
                    className={'icon caret' + (open ? ' open' : '')}
                    aria-hidden
                  />
                  <DomainIcon className="icon sys-icon" aria-hidden />
                  <span className="sys-name">{d.domain}</span>
                  <span className="count">{d.count}</span>
                </button>
                {open && (
                  <div className="tree-children">
                    {d.systems.map(systemGroup)}
                  </div>
                )}
              </div>
            )
          })}
          {rootSystems.map(systemGroup)}
          {ungrouped.length > 0 && (
            <ul className="int-list">
              {ungrouped.map((it) => (
                <IntegrationItem
                  key={it.path}
                  integration={it}
                  active={it.path === selected}
                  onSelect={onSelect}
                />
              ))}
            </ul>
          )}
        </nav>
      )}
    </aside>
  )
}

function IntegrationItem({
  integration: it,
  active,
  onSelect,
}: {
  integration: Integration
  active: boolean
  onSelect: (path: string) => void
}) {
  return (
    <li>
      <button
        className={'int-item' + (active ? ' active' : '')}
        onClick={() => onSelect(it.path)}
      >
        <MemoryIcon className="icon int-icon" aria-hidden />
        <span className="int-text">
          <span className="int-name">{it.name}</span>
          <span className="int-path">{it.path}</span>
        </span>
      </button>
    </li>
  )
}

// groupTree buckets integrations domain → system → items, every level sorted
// alphabetically. Systems without a domain and integrations without a system
// surface at the top level, after the domain groups.
function groupTree(integrations: Integration[]): {
  domains: DomainGroup[]
  rootSystems: SystemGroup[]
  ungrouped: Integration[]
} {
  const bySystem = new Map<string, SystemGroup & { domain: string }>()
  const ungrouped: Integration[] = []
  for (const it of integrations) {
    if (!it.system) {
      ungrouped.push(it)
      continue
    }
    const domain = it.domain ?? ''
    const key = domain + '/' + it.system
    let group = bySystem.get(key)
    if (!group) {
      group = { key, system: it.system, items: [], domain }
      bySystem.set(key, group)
    }
    group.items.push(it)
  }

  const byDomain = new Map<string, DomainGroup>()
  const rootSystems: SystemGroup[] = []
  for (const group of bySystem.values()) {
    if (!group.domain) {
      rootSystems.push(group)
      continue
    }
    let d = byDomain.get(group.domain)
    if (!d) {
      d = { domain: group.domain, systems: [], count: 0 }
      byDomain.set(group.domain, d)
    }
    d.systems.push(group)
    d.count += group.items.length
  }

  const byName = (a: string, b: string) => a.localeCompare(b)
  const domains = [...byDomain.values()].sort((a, b) =>
    byName(a.domain, b.domain),
  )
  for (const d of domains) d.systems.sort((a, b) => byName(a.system, b.system))
  rootSystems.sort((a, b) => byName(a.system, b.system))
  return { domains, rootSystems, ungrouped }
}

function initial(name: string): string {
  return name.trim().charAt(0).toUpperCase() || '?'
}
