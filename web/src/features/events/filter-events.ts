import type { ApiEvent } from '@/shared/api/types'

export function filterEvents(events: ApiEvent[], filter: string): ApiEvent[] {
  const terms = filter.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (terms.length === 0)
    return events
  return events.filter(e => terms.every(t => e.type.toLowerCase().includes(t) || (e.account_id ?? '').toLowerCase().includes(t)))
}
