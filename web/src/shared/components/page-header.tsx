import type { ReactNode } from 'react'

export function PageHeader({ title, description, actions }: { title: ReactNode, description?: ReactNode, actions?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-start justify-between gap-3 border-b px-6 py-4">
      <div className="min-w-0">
        <h1 className="truncate font-heading text-lg font-semibold">{title}</h1>
        {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </header>
  )
}
