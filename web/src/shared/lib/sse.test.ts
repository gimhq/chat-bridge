import type { ApiEvent } from '@/shared/api/types'
import { createSSEParser, streamEvents } from './sse'
import { setToken } from './token'

describe('createSSEParser', () => {
  it('parses fields, comments, multi-line data and split chunks', () => {
    const got: { id?: string, event?: string, data: string }[] = []
    const p = createSSEParser(m => got.push(m))
    p.feed(': keepalive\n\nid: 1\nevent: message.new\ndata: {"a"')
    p.feed(':1}\n\r\nid:2\ndata: line1\ndata: line2\r\n\n')
    p.feed('event: ignored-without-data\n\n')
    expect(got).toEqual([
      { id: '1', event: 'message.new', data: '{"a":1}' },
      { id: '2', event: undefined, data: 'line1\nline2' },
    ])
  })
})

function sseResponse(body: string): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      c.enqueue(new TextEncoder().encode(body))
      c.close()
    },
  })
  return new Response(stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

describe('streamEvents', () => {
  it('delivers events, reconnects with Last-Event-ID and stops', async () => {
    setToken('secret-token')
    const seen: ApiEvent[] = []
    const calls: { url: string, headers: Headers }[] = []
    const statuses: boolean[] = []
    let n = 0
    const fetchImpl = (async (url: string, init?: RequestInit) => {
      calls.push({ url, headers: new Headers(init?.headers) })
      n++
      if (n === 1)
        return sseResponse('id: 0000000000000007\ndata: {"id":"0000000000000007","type":"message.new","timestamp":"t","data":{}}\n\ndata: not-json\n\n')
      if (n === 2)
        return new Response('nope', { status: 500 })
      return new Promise<Response>((_, reject) => init?.signal?.addEventListener('abort', () => reject(new Error('aborted'))))
    }) as unknown as typeof fetch
    const stop = streamEvents({ types: ['message.new', 'request.new'], account: 'a1', onEvent: e => seen.push(e), onStatus: s => statuses.push(s), fetchImpl, retryMs: () => 1 })
    await vi.waitFor(() => expect(calls.length).toBeGreaterThanOrEqual(3))
    stop()
    expect(seen.map(e => e.type)).toEqual(['message.new'])
    expect(calls[0]!.url).toBe('/v1/events/stream?account=a1&types=message.new%2Crequest.new')
    expect(calls[0]!.headers.get('Authorization')).toBe('Bearer secret-token')
    expect(calls[0]!.headers.get('Last-Event-ID')).toBeNull()
    expect(calls[2]!.headers.get('Last-Event-ID')).toBe('0000000000000007')
    expect(statuses[0]).toBe(true)
    expect(statuses).toContain(false)
  })

  it('starts after the given event id', async () => {
    setToken('tok')
    const headers: Headers[] = []
    const fetchImpl = (async (_url: string, init?: RequestInit) => {
      headers.push(new Headers(init?.headers))
      return new Promise<Response>((_, reject) => init?.signal?.addEventListener('abort', () => reject(new Error('aborted'))))
    }) as unknown as typeof fetch
    const stop = streamEvents({ since: '0000000000000042', onEvent: () => {}, fetchImpl })
    await vi.waitFor(() => expect(headers).toHaveLength(1))
    stop()
    expect(headers[0]!.get('Last-Event-ID')).toBe('0000000000000042')
  })
})
