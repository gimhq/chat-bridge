import type { TokenScope } from '@/shared/api/types'

export const emptyScope: TokenScope = { persons: [], contacts: [], chats: [], read_only: false }

/** One line describing how much a scope lists, for the token table. */
export function scopeSummary(s: TokenScope): string {
  const parts = [
    s.persons.length > 0 && `${s.persons.length} 个人`,
    s.contacts.length > 0 && `${s.contacts.length} 个联系人`,
    s.chats.length > 0 && `${s.chats.length} 个会话`,
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(' · ') : '空（看不到任何内容）'
}
