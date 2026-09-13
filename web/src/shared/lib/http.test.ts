import { api, apiBlob, ApiError, buildUrl, errorMessage, seg } from './http'
import { getToken, setToken } from './token'

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function failure(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p
  }
  catch (e) {
    return e as ApiError
  }
  throw new Error('expected the request to fail')
}

describe('http client', () => {
  afterEach(() => vi.restoreAllMocks())

  it('builds urls and encodes ids', () => {
    expect(buildUrl('/accounts', { limit: 5, cursor: '', q: undefined, backfill: true })).toBe('/v1/accounts?limit=5&backfill=true')
    expect(`/chats/${seg('!room:example.org/x')}`).toBe('/chats/!room%3Aexample.org%2Fx')
  })

  it('sends the token and JSON, decodes JSON and 204', async () => {
    setToken('  tok-123  ')
    const spy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(json(200, { ok: 1 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    expect(await api<{ ok: number }>('/x', { method: 'POST', body: { a: 1 } })).toEqual({ ok: 1 })
    const [, init] = spy.mock.calls[0]!
    const headers = new Headers(init!.headers)
    expect(headers.get('Authorization')).toBe('Bearer tok-123')
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(init!.body).toBe('{"a":1}')
    expect(await api('/y', { method: 'DELETE' })).toBeUndefined()
  })

  it('passes FormData through without a JSON content type', async () => {
    setToken('tok')
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(json(201, { media_id: 'upl_1' }))
    const fd = new FormData()
    fd.set('file', new Blob(['x']), 'a.txt')
    await api('/accounts/a1/media', { method: 'POST', body: fd })
    const [, init] = spy.mock.calls[0]!
    expect(init!.body).toBe(fd)
    expect(new Headers(init!.headers).get('Content-Type')).toBeNull()
  })

  it('maps error envelopes and clears the token on 401', async () => {
    setToken('tok')
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(json(409, { error: { code: 'account_not_ready', message: 'account is connecting' } }))
      .mockResolvedValueOnce(new Response('boom', { status: 502, statusText: 'Bad Gateway' }))
      .mockResolvedValueOnce(json(401, { error: { code: 'unauthorized', message: 'invalid token' } }))
    const e1 = await failure(api('/a'))
    expect(e1).toBeInstanceOf(ApiError)
    expect(e1.status).toBe(409)
    expect(errorMessage(e1)).toBe('account is connecting (account_not_ready)')
    const e2 = await failure(api('/b'))
    expect(e2.code).toBe('http_error')
    expect(e2.message).toBe('502 Bad Gateway')
    await api('/c').catch(() => {})
    expect(getToken()).toBe('')
    expect(errorMessage(new Error('plain'))).toBe('plain')
    expect(errorMessage('str')).toBe('str')
  })

  it('fetches blobs with the token', async () => {
    setToken('tok')
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('PNG', { headers: { 'Content-Type': 'image/png' } }))
    const blob = await apiBlob('/media/m1')
    expect(await blob.text()).toBe('PNG')
    expect(spy.mock.calls[0]![0]).toBe('/v1/media/m1')
  })
})
