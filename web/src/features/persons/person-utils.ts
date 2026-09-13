import type { PersonLink } from '@/shared/api/types'

/** Tags are entered comma separated (ASCII or full-width); blanks and duplicates drop out. */
export function parseTags(raw: string): string[] {
  return [...new Set(raw.split(/[,，]/).map(t => t.trim()).filter(Boolean))]
}

export function linkLabel(l: Pick<PersonLink, 'account_id' | 'user_id' | 'platform' | 'name'>): string {
  return `${l.platform || l.account_id} · ${l.name || l.user_id}`
}

export const sourceLabels: Record<PersonLink['source'], string> = {
  manual: '手动',
  phone: '同手机号',
}
