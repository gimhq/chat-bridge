import type { Token } from '@/shared/api/types'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { scopeSummary } from './token-utils'
import { TokensCard } from './tokens-card'

const reader: Token = {
  id: 'tok_1',
  name: 'reader',
  scope: { persons: ['per_1'], contacts: [{ account_id: 'wa', user_id: 'u1' }], chats: [], read_only: true },
  created_at: '2026-10-03T16:54:00Z',
  last_used_at: null,
}

const lists = {
  'GET /v1/persons': () => ({ persons: [{ id: 'per_1', name: 'Alice', tags: [], notes: '', links: [], channels: [], created_at: 't', updated_at: 't' }] }),
  'GET /v1/accounts': () => ({ accounts: [{ id: 'wa', platform: 'whatsapp', status: 'connected' }] }),
  'GET /v1/accounts/wa/contacts': () => ({ contacts: [{ id: 'u2', name: 'Bob', handle: '+2' }, { id: 'me', name: 'Me', is_self: true }] }),
  'GET /v1/accounts/wa/chats': () => ({ chats: [{ id: 'g1', kind: 'group', name: 'Team' }] }),
}

describe('tokens card', () => {
  afterEach(() => vi.restoreAllMocks())

  it('summarises a scope', () => {
    expect(scopeSummary(reader.scope)).toBe('1 个人 · 1 个联系人')
    expect(scopeSummary({ persons: [], contacts: [], chats: [], read_only: false })).toBe('空（看不到任何内容）')
  })

  it('lists tokens and revokes one after confirmation', async () => {
    const api = mockApi({ 'GET /v1/tokens': () => ({ tokens: [reader] }), 'DELETE /v1/tokens/tok_1': () => undefined })
    renderWithClient(<TokensCard />)
    expect(await screen.findByText('reader')).toBeInTheDocument()
    expect(screen.getByText('只读')).toBeInTheDocument()
    expect(screen.getByText('从未')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '吊销 reader' }))
    expect(api.calls.some(c => c.method === 'DELETE')).toBe(false)
    fireEvent.click(await screen.findByRole('button', { name: '吊销' }))
    await waitFor(() => expect(api.calls.some(c => c.method === 'DELETE' && c.path === '/v1/tokens/tok_1')).toBe(true))
  })

  it('creates a token from the pickers and shows the secret once', async () => {
    const api = mockApi({
      ...lists,
      'GET /v1/tokens': () => ({ tokens: [] }),
      'POST /v1/tokens': body => ({ ...reader, ...(body as object), id: 'tok_2', token: 'cbt_secret' }),
    })
    renderWithClient(<TokensCard />)
    fireEvent.click(await screen.findByRole('button', { name: '新建' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('button', { name: '创建' })).toBeDisabled()

    fireEvent.change(within(dialog).getByLabelText('名称'), { target: { value: ' family ' } })
    fireEvent.click(within(dialog).getByRole('switch', { name: '只读' }))
    fireEvent.click(await within(dialog).findByRole('button', { name: 'Alice' }))
    fireEvent.click(await within(dialog).findByRole('button', { name: '添加联系人 Bob' }))
    fireEvent.click(await within(dialog).findByRole('button', { name: '添加会话 Team' }))
    expect(within(dialog).queryByRole('button', { name: '添加联系人 Me' })).not.toBeInTheDocument()
    expect(within(dialog).getByText('联系人 wa · u2')).toBeInTheDocument()

    // A chip removes its entry again.
    fireEvent.click(within(dialog).getByRole('button', { name: '移除 会话 wa · g1' }))
    fireEvent.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() => expect(api.calls.some(c => c.method === 'POST')).toBe(true))
    expect(api.calls.find(c => c.method === 'POST')!.body).toEqual({
      name: 'family',
      scope: { persons: ['per_1'], contacts: [{ account_id: 'wa', user_id: 'u2' }], chats: [], read_only: true },
    })
    expect(await screen.findByLabelText('令牌密钥')).toHaveValue('cbt_secret')
  })

  it('edits a token with its current scope', async () => {
    const api = mockApi({ ...lists, 'GET /v1/tokens': () => ({ tokens: [reader] }), 'PATCH /v1/tokens/tok_1': () => reader })
    renderWithClient(<TokensCard />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑 reader' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('名称')).toHaveValue('reader')
    fireEvent.click(within(dialog).getByRole('button', { name: '移除 联系人 wa · u1' }))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.calls.some(c => c.method === 'PATCH')).toBe(true))
    expect(api.calls.find(c => c.method === 'PATCH')!.body).toEqual({ name: 'reader', scope: { persons: ['per_1'], contacts: [], chats: [], read_only: true } })
  })
})
