import type { ComponentType, ReactNode, SVGProps } from 'react'

type IconComponent = ComponentType<SVGProps<SVGSVGElement>>

/** One labelled fact of a header grid. */
export function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="meta">
      <span className="meta-label">{label}</span>
      <span className="meta-value">{value}</span>
    </div>
  )
}

/** A titled block of a detail page, with an optional leading icon. */
export function Section({
  title,
  icon: Icon,
  children,
}: {
  title: string
  icon?: IconComponent
  children: ReactNode
}) {
  return (
    <section className="section">
      <h2 className="section-title">
        {Icon && <Icon className="icon" aria-hidden />}
        {title}
      </h2>
      {children}
    </section>
  )
}
