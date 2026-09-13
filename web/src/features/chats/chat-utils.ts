import type { ContentType, Message } from '@/shared/api/types'

export function initials(name: string): string {
  const trimmed = name.replace(/^[@+!#]/, '').trim()
  return (trimmed.slice(0, 2) || '?').toUpperCase()
}

export function parseMembers(raw: string): string[] {
  return [...new Set(raw.split(/[\s,;，；]+/).map(s => s.trim()).filter(Boolean))]
}

export function plainText(text: string, format?: string): string {
  if (format !== 'html')
    return text
  const doc = new DOMParser().parseFromString(text.replace(/<br\s*\/?>/gi, '\n'), 'text/html')
  return doc.body.textContent ?? ''
}

export function groupReactions(reactions: Message['reactions']): { emoji: string, count: number, senders: string[] }[] {
  const map = new Map<string, string[]>()
  for (const r of reactions ?? [])
    map.set(r.emoji, [...(map.get(r.emoji) ?? []), r.sender_id])
  return [...map].map(([emoji, senders]) => ({ emoji, count: senders.length, senders }))
}

export function contentTypeFor(mime: string): ContentType {
  if (mime.startsWith('image/'))
    return 'image'
  if (mime.startsWith('video/'))
    return 'video'
  if (mime.startsWith('audio/'))
    return 'audio'
  return 'file'
}
