import type { LoginField, Platform } from '@/shared/api/types'

export function buildConfig(platform: Platform | undefined, values: Record<string, string | boolean>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [key, prop] of Object.entries(platform?.config_schema?.properties ?? {})) {
    const v = values[key]
    if (v === undefined || v === '')
      continue
    if (prop.type === 'integer' || prop.type === 'number')
      out[key] = Number(v)
    else if (prop.type === 'boolean')
      out[key] = Boolean(v)
    else
      out[key] = v
  }
  return out
}

export function inputProps(f: LoginField) {
  switch (f.type) {
    case 'phone': return { type: 'tel', autoComplete: 'tel', inputMode: 'tel' as const }
    case 'password': return { type: 'password', autoComplete: 'current-password' }
    case 'code': return { type: 'text', autoComplete: 'one-time-code', inputMode: 'numeric' as const }
    case 'url': return { type: 'url', autoComplete: 'url' }
    default: return { type: 'text', autoComplete: 'off' }
  }
}

export function safeHttpUrl(raw: string): string | undefined {
  try {
    const u = new URL(raw)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.toString() : undefined
  }
  catch {
    return undefined
  }
}
