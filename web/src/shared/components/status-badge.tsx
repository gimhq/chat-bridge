import type { AccountStatus } from '@/shared/api/types'
import { Badge } from '@/shared/components/ui/badge'
import { statusLabels, statusTone } from '@/shared/lib/format'
import { cn } from '@/shared/lib/utils'

const dot = { ok: 'bg-success', warn: 'bg-warning', bad: 'bg-destructive', idle: 'bg-muted-foreground' } as const

export function StatusBadge({ status }: { status: AccountStatus }) {
  return (
    <Badge variant="outline" className="gap-1.5">
      <span className={cn('size-1.5 rounded-full', dot[statusTone(status)])} aria-hidden />
      {statusLabels[status] ?? status}
    </Badge>
  )
}
