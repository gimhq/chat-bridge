import type { ApiErrorBody } from '@/shared/api/types'
import { clearToken, getToken } from './token'

export const API_BASE = '/v1'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details?: Record<string, unknown>

  constructor(status: number, body: ApiErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.details = body.details
  }
}

export type Query = Record<string, string | number | boolean | undefined | null>

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  query?: Query
  signal?: AbortSignal
}

/** Percent-encodes one path segment (chat and user ids carry '@', '!', ':' and '/'). */
export function seg(value: string): string {
  return encodeURIComponent(value)
}

export function buildUrl(path: string, query?: Query): string {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v !== undefined && v !== null && v !== '')
      qs.set(k, String(v))
  }
  const s = qs.toString()
  return `${API_BASE}${path}${s ? `?${s}` : ''}`
}

async function errorOf(res: Response): Promise<ApiError> {
  let body: ApiErrorBody = { code: 'http_error', message: `${res.status} ${res.statusText}` }
  try {
    const parsed = await res.json() as { error?: ApiErrorBody }
    if (parsed.error?.code)
      body = parsed.error
  }
  catch {}
  return new ApiError(res.status, body)
}

async function send(path: string, opts: RequestOptions): Promise<Response> {
  const headers = new Headers()
  const token = getToken()
  if (token)
    headers.set('Authorization', `Bearer ${token}`)
  let body: BodyInit | undefined
  if (opts.body instanceof FormData) {
    body = opts.body
  }
  else if (opts.body !== undefined) {
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(opts.body)
  }
  const res = await fetch(buildUrl(path, opts.query), { method: opts.method ?? 'GET', headers, body, signal: opts.signal })
  if (res.status === 401)
    clearToken()
  if (!res.ok)
    throw await errorOf(res)
  return res
}

/** Calls the API and decodes JSON; 204 resolves to undefined. */
export async function api<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await send(path, opts)
  if (res.status === 204)
    return undefined as T
  return await res.json() as T
}

/** Fetches bytes with the token (media cannot use <img src> because it needs Authorization). */
export async function apiBlob(path: string, signal?: AbortSignal): Promise<Blob> {
  const res = await send(path, { signal })
  return await res.blob()
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError)
    return `${err.message} (${err.code})`
  if (err instanceof Error)
    return err.message
  return String(err)
}
