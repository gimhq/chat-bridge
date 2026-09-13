import type { ChatRequest } from '@/shared/api/types'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { RequestList } from './requests-page'

const base = { account_id: 'a1', message: undefined, created_at: '2026-09-13T10:00:00Z', expires_at: null, answered_at: null }
const call: ChatRequest = { ...base, id: 'req_call', kind: 'call', state: 'pending', from: { id: 'u1', name: 'Alice' }, chat: { id: 'u1' }, call: { kind: 'video' }, actions: ['ignore'] }
const invite: ChatRequest = { ...base, id: 'req_inv', kind: 'chat_invite', state: 'pending', from: { id: 'u2' }, chat: { id: 'g1', name: 'Team' }, message: 'join us', actions: ['accept', 'reject', 'ignore'] }
const done: ChatRequest = { ...base, id: 'req_done', kind: 'join_request', state: 'accepted', from: { id: 'u3' }, chat: { id: 'g2' }, actions: [], answered_at: '2026-09-13T11:00:00Z' }

describe('requestList', () => {
  afterEach(() => vi.restoreAllMocks())

  it('offers the allowed actions and answers through the API', async () => {
    const api = mockApi({
      'POST /v1/accounts/a1/requests/req_call/ignore': () => ({ ...call, state: 'ignored', actions: [] }),
      'POST /v1/accounts/a1/requests/req_inv/accept': () => ({ ...invite, state: 'accepted', actions: [] }),
    })
    renderWithClient(<RequestList requests={[call, invite, done]} loading={false} showAccount />)
    expect(screen.getByText('视频来电')).toBeInTheDocument()
    expect(screen.getByText(/来自 Alice/)).toBeInTheDocument()
    expect(screen.getByText(/Team · “join us”/)).toBeInTheDocument()
    expect(screen.getByText('已接受')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: '接受' })).toHaveLength(1)
    expect(screen.getAllByRole('button', { name: '拒绝' })).toHaveLength(1) // calls are never rejected from the bridge
    const ignores = screen.getAllByRole('button', { name: '忽略' })
    expect(ignores).toHaveLength(2)

    fireEvent.click(ignores[0]!)
    await waitFor(() => expect(api.calls.some(c => c.path === '/v1/accounts/a1/requests/req_call/ignore')).toBe(true))
    fireEvent.click(screen.getByRole('button', { name: '接受' }))
    await waitFor(() => expect(api.calls.some(c => c.path === '/v1/accounts/a1/requests/req_inv/accept')).toBe(true))
  })

  it('renders loading and empty states', () => {
    const { rerender } = renderWithClient(<RequestList requests={[]} loading />)
    expect(screen.queryByText('没有请求')).not.toBeInTheDocument()
    rerender(<RequestList requests={[]} loading={false} />)
    expect(screen.getByText('没有请求')).toBeInTheDocument()
  })
})
