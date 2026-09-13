import { fireEvent, screen, waitFor } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { Composer } from './composer'

const sent = { id: 's1', account_id: 'a1', chat_id: 'c1', sender: { id: 'me' }, from_me: true, timestamp: 't', content: { type: 'text' }, mentions: [], forwarded: false, ephemeral: null, edited_at: null, deleted_at: null, reactions: [] }

describe('composer', () => {
  afterEach(() => vi.restoreAllMocks())

  it('sends text on Enter but not on Shift+Enter', async () => {
    const api = mockApi({ 'POST /v1/accounts/a1/chats/c1/messages': () => sent })
    const onClearReply = vi.fn()
    renderWithClient(<Composer accountId="a1" chatId="c1" disabled={false} onClearReply={onClearReply} />)
    const box = screen.getByLabelText('消息内容')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter', shiftKey: true })
    expect(api.calls).toHaveLength(0)
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.calls).toHaveLength(1))
    const body = api.calls[0]!.body as { client_id: string, content: unknown }
    expect(body.content).toEqual({ type: 'text', text: 'hello' })
    expect(body.client_id).toMatch(/^ui-/)
    await waitFor(() => expect(box).toHaveValue(''))
    expect(onClearReply).toHaveBeenCalled()
  })

  it('uploads a file then sends it as media', async () => {
    const api = mockApi({
      'POST /v1/accounts/a1/media': () => ({ media_id: 'upl_1', mime: 'image/png', state: 'ready' }),
      'POST /v1/accounts/a1/chats/c1/messages': () => sent,
    })
    const { container } = renderWithClient(<Composer accountId="a1" chatId="c1" disabled={false} onClearReply={() => {}} />)
    const input = container.querySelector('input[type=file]')!
    fireEvent.change(input, { target: { files: [new File(['PNG'], 'a.png', { type: 'image/png' })] } })
    expect(screen.getByText('a.png')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(api.calls.map(c => c.path)).toEqual(['/v1/accounts/a1/media', '/v1/accounts/a1/chats/c1/messages']))
    expect(api.calls[0]!.body).toBeInstanceOf(FormData)
    expect((api.calls[1]!.body as { content: unknown }).content).toEqual({ type: 'image', attachments: [{ media_id: 'upl_1', mime: 'image/png', state: 'ready' }] })
  })

  it('explains why sending is disabled', () => {
    renderWithClient(<Composer accountId="a1" chatId="c1" disabled onClearReply={() => {}} />)
    expect(screen.getByText('账号未连接，暂时无法发送。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
  })
})
