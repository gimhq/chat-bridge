import type { ApiEvent } from '@/shared/api/types'
import { buildUrl } from './http'
import { getToken } from './token'

export interface SSEMessage {
  id?: string
  event?: string
  data: string
}

/**
 * Incremental text/event-stream parser (WHATWG rules: `field: value` lines, blank line dispatches,
 * `:` comments, multi-line data joined by '\n'). Needed because EventSource cannot send headers.
 */
export function createSSEParser(onMessage: (m: SSEMessage) => void) {
  let buffer = ''
  let data: string[] = []
  let id: string | undefined
  let event: string | undefined

  function dispatch() {
    if (data.length > 0)
      onMessage({ id, event, data: data.join('\n') })
    data = []
    event = undefined
  }

  function line(raw: string) {
    if (raw === '') {
      dispatch()
      return
    }
    if (raw.startsWith(':'))
      return
    const colon = raw.indexOf(':')
    const field = colon === -1 ? raw : raw.slice(0, colon)
    let value = colon === -1 ? '' : raw.slice(colon + 1)
    if (value.startsWith(' '))
      value = value.slice(1)
    if (field === 'data')
      data.push(value)
    else if (field === 'id')
      id = value
    else if (field === 'event')
      event = value
  }

  return {
    feed(chunk: string) {
      buffer += chunk
      let nl = buffer.search(/\r\n|\r|\n/)
      while (nl !== -1) {
        const sep = buffer[nl] === '\r' && buffer[nl + 1] === '\n' ? 2 : 1
        line(buffer.slice(0, nl))
        buffer = buffer.slice(nl + sep)
        nl = buffer.search(/\r\n|\r|\n/)
      }
    },
  }
}

export interface StreamOptions {
  /** Resume after this event id; without it the server replays retained history. */
  since?: string
  account?: string
  types?: string[]
  onEvent: (e: ApiEvent) => void
  onStatus?: (connected: boolean) => void
  /** Injected for tests. */
  fetchImpl?: typeof fetch
  retryMs?: (attempt: number) => number
}

const defaultRetry = (attempt: number) => Math.min(30_000, 1000 * 2 ** Math.min(attempt, 5))

/**
 * Follows /v1/events/stream until the returned stop function is called, reconnecting with
 * backoff and resuming from the last seen event id.
 */
export function streamEvents(opts: StreamOptions): () => void {
  const ctrl = new AbortController()
  const doFetch = opts.fetchImpl ?? fetch
  const retry = opts.retryMs ?? defaultRetry
  let lastId: string | undefined = opts.since || undefined
  let attempt = 0

  async function run() {
    while (!ctrl.signal.aborted) {
      try {
        const headers = new Headers({ Accept: 'text/event-stream', Authorization: `Bearer ${getToken()}` })
        if (lastId)
          headers.set('Last-Event-ID', lastId)
        const res = await doFetch(buildUrl('/events/stream', { account: opts.account, types: opts.types?.join(',') }), { headers, signal: ctrl.signal })
        if (!res.ok || !res.body)
          throw new Error(`event stream: ${res.status}`)
        attempt = 0
        opts.onStatus?.(true)
        const parser = createSSEParser((m) => {
          if (m.id)
            lastId = m.id
          try {
            opts.onEvent(JSON.parse(m.data) as ApiEvent)
          }
          catch {}
        })
        const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
        for (;;) {
          const { value, done } = await reader.read()
          if (done)
            break
          parser.feed(value)
        }
      }
      catch {
        if (ctrl.signal.aborted)
          return
      }
      opts.onStatus?.(false)
      await new Promise(resolve => setTimeout(resolve, retry(attempt++)))
    }
  }

  void run()
  return () => ctrl.abort()
}
